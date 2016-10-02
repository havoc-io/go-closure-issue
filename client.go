package rsync

import (
	"bytes"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"bitbucket.org/kardianos/rsync"
)

const (
	maxOutstandingStagingRequests = 4
	stagingTempFileBaseName       = "staging"
)

type readSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type emptyReadSeekCloser struct {
	*bytes.Reader
}

func newEmptyReadSeekCloser() readSeekCloser {
	return &emptyReadSeekCloser{bytes.NewReader(nil)}
}

func (e *emptyReadSeekCloser) Close() error {
	return nil
}

type dispatchedRequest struct {
	path Path
	base readSeekCloser
}

type Client struct {
	root            string
	staging         string
	stream          *Stream
	dispatchRsyncer *rsync.RSync
	receiveRsyncer  *rsync.RSync
	stagingHash     hash.Hash
}

func NewClient(root, staging string, stagingHash hash.Hash, connection io.ReadWriteCloser) *Client {
	return &Client{
		root:            root,
		staging:         staging,
		stagingHash:     stagingHash,
		stream:          newStream(connection),
		dispatchRsyncer: newRsyncer(),
		receiveRsyncer:  newRsyncer(),
	}
}

func (c *Client) dispatch(paths []Path, outstanding chan<- dispatchedRequest, cancel <-chan struct{}) error {
	// Loop over paths and dispatch.
	for _, path := range paths {
		// Open the base. If this fails (which it might if the file doesn't
		// exist), then simply use an empty base.
		var base readSeekCloser
		if f, err := os.Open(path.AppendedToRoot(c.root)); err != nil {
			base = newEmptyReadSeekCloser()
		} else {
			base = f
		}

		// Compute the base signature. If there is an error, just abort, because
		// most likely the file is being modified concurrently and we'll have to
		// stage again later. We don't treat this as terminal though.
		var signature []rsync.BlockHash
		writer := func(b rsync.BlockHash) error {
			signature = append(signature, b)
			return nil
		}
		if c.dispatchRsyncer.CreateSignature(base, writer) != nil {
			base.Close()
			continue
		}

		// Send the request.
		if err := c.stream.Encode(request{path, signature}); err != nil {
			return err
		}

		// Send the request to the receiver, but watch for cancellation.
		select {
		case outstanding <- dispatchedRequest{path, base}:
		case <-cancel:
			base.Close()
			break
		}
	}

	// Notify the receiver that we're done sending requests.
	close(outstanding)

	// Success.
	return nil
}

func (c *Client) burnRemainingOperations() error {
	for {
		var response response
		if err := c.stream.Decode(&response); err != nil {
			return err
		} else if response.Done {
			return nil
		}
	}
}

func (c *Client) receive(outstanding <-chan dispatchedRequest, cancel <-chan struct{}) error {
	// Loop until we've processed all outstanding requests or been cancelled.
	for {
		// Grab the next request, watching for closure of outstanding or
		// cancellation.
		var dispatchedRequest dispatchedRequest
		select {
		case dispatchedRequest = <-outstanding:
			if dispatchedRequest.base == nil {
				return nil
			}
		case <-cancel:
			return nil
		}

		// TODO: Perform a staging update.

		// Create a temporary file to record the output. If we can't open
		// temporary files, that's a terminal error.
		target, err := ioutil.TempFile(c.staging, stagingTempFileBaseName)
		if err != nil {
			dispatchedRequest.base.Close()
			return err
		}

		// Create channels to communicate with the ApplyDelta Goroutine.
		operations := make(chan rsync.Operation)
		applyErrors := make(chan error, 1)

		// Reset the hash state.
		c.stagingHash.Reset()

		// Start the ApplyDelta operation in a separate Goroutine, recording the
		// hash of the received contents.
		go func() {
			applyErrors <- c.receiveRsyncer.ApplyDelta(
				target,
				dispatchedRequest.base,
				operations,
				c.stagingHash,
			)
		}()

		// Read and feed operations into the Goroutine, watching for errors.
		var applyError, decodeError error
		applyExited := false
		for {
			// Grab the next operation.
			var response response
			if err = c.stream.Decode(&response); err != nil {
				decodeError = err
				break
			}

			// Check if the operation stream is done.
			if response.Done {
				break
			}

			// Forward the operation. If there is an error, burn the remaining
			// operations in this stream.
			select {
			case operations <- response.Operation:
			case applyError = <-applyErrors:
				applyExited = true
				decodeError = c.burnRemainingOperations()
				break
			}
		}

		// Tell the ApplyDelta Goroutine that operations are complete. It may
		// have exited already if there was an error, in which case this will
		// have no effect.
		close(operations)

		// Ensure that the Goroutine has completed. We use a separate boolean to
		// track whether or not applyError was actually set, because it's a bit
		// more robust than simply checking for a nil error. This is probably
		// overkill, because ApplyDelta won't return a nil error before
		// operations is closed, and therefore a nil applyError wouldn't be set
		// by the loop above, so we could probably just check if applyError is
		// nil here, but that behavior is not guaranteed in the rsync
		// documentation, so it's easier to just check explicitly whether or not
		// it has been set.
		if !applyExited {
			applyError = <-applyErrors
		}

		// Close the target.
		target.Close()

		// If there was an error from any source, simply remove the file,
		// otherwise stage it.
		if decodeError != nil || applyError != nil {
			os.Remove(target.Name())
		} else {
			name := fmt.Sprintf("%x", c.stagingHash.Sum(nil))
			os.Rename(target.Name(), filepath.Join(c.staging, name))
		}

		// If there was a decode error, then we're toast.
		if decodeError != nil {
			return decodeError
		}
	}

	// Unreachable.
	panic("unreachable")
}

func (c *Client) Stage(paths []Path) error {
	// Create pipeline channels.
	outstanding := make(chan dispatchedRequest, maxOutstandingStagingRequests)
	dispatchErrors := make(chan error)
	receiveErrors := make(chan error)
	dispatchCancel := make(chan struct{}, 1)
	receiveCancel := make(chan struct{}, 1)

	// Start the pipeline.
	go func() {
		dispatchErrors <- c.dispatch(paths, outstanding, dispatchCancel)
	}()
	go func() {
		receiveErrors <- c.receive(outstanding, receiveCancel)
	}()

	// Wait for both Goroutines to exit. If there is an error, then cancel,
	// because the other Goroutine will fail to exit.
	dispatchRunning := true
	receiveRunning := true
	var dispatchError, receiveError error
	for dispatchRunning || receiveRunning {
		select {
		case dispatchError = <-dispatchErrors:
			dispatchRunning = false
			if dispatchError != nil {
				receiveCancel <- struct{}{}
			}
		case receiveError = <-receiveErrors:
			receiveRunning = false
			if receiveError != nil {
				dispatchCancel <- struct{}{}
			}
		}
	}

	// If there was an error, there may be outstanding requests in the queue
	// that weren't cleaned up, so take care of them.
	for dispatched := range outstanding {
		dispatched.base.Close()
	}

	// If there was an error, return it.
	if dispatchError != nil {
		return dispatchError
	} else if receiveError != nil {
		return receiveError
	}

	// Success.
	return nil
}

func (c *Client) Close() error {
	return c.stream.Close()
}
