package rsync

import (
	"crypto/sha1"

	"bitbucket.org/kardianos/rsync"
)

func newRsyncer() *rsync.RSync {
	return &rsync.RSync{UniqueHasher: sha1.New()}
}

type request struct {
	Path          Path
	BaseSignature []rsync.BlockHash
}

type response struct {
	Operation rsync.Operation
	Done      bool
}
