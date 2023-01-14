package memory

import "github.com/radekg/boos/pkg/storage/types"

type done struct {
	s chan struct{}
	f chan error
}

type internalWriterDone interface {
	types.WriterDone
	succeeded()
	failed(e error)
}

func newDone() internalWriterDone {
	return &done{
		s: make(chan struct{}),
		f: make(chan error),
	}
}

func (d *done) Success() <-chan struct{} {
	return d.s
}

func (d *done) Fail() <-chan error {
	return d.f
}

func (d *done) succeeded() {
	close(d.s)
}

func (d *done) failed(e error) {
	d.f <- e
}
