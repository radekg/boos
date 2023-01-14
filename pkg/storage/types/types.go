package types

import (
	"context"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

var storageLogger = hclog.Default()

func SetStorageLogger(l hclog.Logger) {
	storageLogger = l
}

func GetStorageLogger() hclog.Logger {
	return storageLogger
}

type SampleWithTimingHint interface {
	Sample() media.Sample
	TimingHint() time.Duration
}

type SamplingReader interface {
	MimeType() string
	NextSample() (SampleWithTimingHint, error)
}

type Backend interface {
	Configure(settings map[string]interface{}, logger hclog.Logger) error
	Read(ctx context.Context, key string) (SamplingReader, error)
	Write(ctx context.Context, key string, track *webrtc.TrackRemote) (WriterDone, error)
}

type WriterDone interface {
	Success() <-chan struct{}
	Fail() <-chan error
}
