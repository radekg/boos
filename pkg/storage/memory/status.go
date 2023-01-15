package memory

import "github.com/radekg/boos/pkg/storage/types"

type status struct {
	s chan struct{}
	f chan error
}

type internalWriterStatus interface {
	types.WriterStatus
	succeeded()
	failed(e error)
}

func newStatus() internalWriterStatus {
	return &status{
		s: make(chan struct{}),
		f: make(chan error),
	}
}

func (d *status) Success() <-chan struct{} {
	return d.s
}

func (d *status) Fail() <-chan error {
	return d.f
}

func (d *status) succeeded() {
	close(d.s)
}

func (d *status) failed(e error) {
	d.f <- e
}
