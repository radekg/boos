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
	MimeType() string
	NextSample() (SampleWithTimingHint, error)
}

// Backend represents storage backend.
type Backend interface {
	Configure(settings map[string]interface{}, logger hclog.Logger) error
	Read(ctx context.Context, key string) (SamplingReader, error)
	Write(ctx context.Context, key string, track *webrtc.TrackRemote) (WriterStatus, error)
}

// WriterStatus contains a status of a track write operation.
type WriterStatus interface {
	Success() <-chan struct{}
	Fail() <-chan error
}
