package rsync

import (
	"testing"
	"fmt"
	"io/ioutil"
	"runtime"
	"path/filepath"
	"io"
	"os"
	"net"
	"crypto/sha1"
)

const (
	godocChunkSize = 100000
)

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("%s.exe", name)
	}
	return name
}

func exePath(name string) Path {
	return Path{exeName(name)}
}

func sha1Digest(path string) ([]byte, error) {
	if f, err := os.Open(path); err != nil {
		return nil, err
	} else {
		hasher := sha1.New()
		_, err := io.Copy(hasher, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		return hasher.Sum(nil), err
	}
}

func TestSyncing(t *testing.T) {
	// Compute the path to the GOROOT bin directory.
	bin := filepath.Join(runtime.GOROOT(), "bin")

	// Create a temporary directory that will serve as both our root and staging
	// directory. Ensure that it's cleaned up.
	target, err := ioutil.TempDir("", "rsync")
	if err != nil {
		t.Fatal("couldn't create temporary staging directory:", err)
	}
	defer os.RemoveAll(target)

	// Copy part of godoc into the directory.
	godocPart, err := os.Create(filepath.Join(target, exeName("godoc")))
	if err != nil {
		t.Fatal("couldn't open base file for writing:", err)
	}
	godoc, err := os.Open(filepath.Join(bin, exeName("godoc")))
	if err != nil {
		godocPart.Close()
		t.Fatal("couldn't open godoc for reading:", err)
	}
	godocChunk := io.LimitReader(godoc, godocChunkSize)
	_, err = io.Copy(godocPart, godocChunk)
	godocPart.Close()
	godoc.Close()
	if err != nil {
		t.Fatal("unable to copy godoc chunk:", err)
	}

	// Create an in-memory connection.
	serverConnection, clientConnection := net.Pipe()

	// Create and start a server.
	server := NewServer(bin, serverConnection)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve()
	}()

	// Create a client.
	client := NewClient(target, target, sha1.New(), clientConnection)

	// Compute the paths that we want to stage.
	paths := []Path{exePath("go"), exePath("godoc"), exePath("gofmt")}

	// Perform staging.
	if err = client.Stage(paths); err != nil {
		server.Close()
		<-serverErrors
		t.Fatal("unable to stage paths:", err)
	}

	// Shutdown the server. This should cause the serve method to return an
	// error (EOF).
	server.Close()
	if serverError := <-serverErrors; serverError == nil {
		t.Error("expected serve error to be non-nil")
	}

	// Close the client.
	if err = client.Close(); err != nil {
		t.Error("client did not close cleanly:", err)
	}

	// TODO: Check digest matches.
}
