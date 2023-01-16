package types

import (
	"context"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

// SampleWithTimingHint contains a media sample to send to the remote with a timing delay hint.
type SampleWithTimingHint interface {
	Sample() media.Sample
	TimingHint() time.Duration
}

// SamplingReader reads samples.
type SamplingReader interface {
	// MimeType() string
	NextSample() (SampleWithTimingHint, error)
}

// Backend represents storage backend.
type Backend interface {
	Configure(settings map[string]interface{}, logger hclog.Logger) error
	Contains(key string) bool
	Audio(key string) (ReaderStatus, error)
	Video(key string) (ReaderStatus, error)
	Write(ctx context.Context, key string, track *webrtc.TrackRemote) (WriterStatus, error)
}

type ReaderStatus interface {
	HasData() bool
	MimeType() string
	Reader() SamplingReader
}

// WriterStatus contains a status of a track write operation.
type WriterStatus interface {
	Success() <-chan struct{}
	Fail() <-chan error
}
