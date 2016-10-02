package rsync

import (
	"encoding/gob"
	"io"
)

type Stream struct {
	*gob.Decoder
	*gob.Encoder
	io.Closer
}

func newStream(raw io.ReadWriteCloser) *Stream {
	return &Stream{gob.NewDecoder(raw), gob.NewEncoder(raw), raw}
}
