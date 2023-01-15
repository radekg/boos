package memory

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/pion/webrtc/v2/pkg/media/h264writer"
	"github.com/pion/webrtc/v2/pkg/media/oggwriter"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/radekg/boos/pkg/media/ivfwriter"
	"github.com/radekg/boos/pkg/storage/types"
)

type internalItem struct {
	audioMimeType string
	videoMimeType string
	audioBuf      *bytes.Buffer
	videoBuf      *bytes.Buffer
}

type storageBackend struct {
	logger  hclog.Logger
	backend map[string]*internalItem
	lock    *sync.RWMutex
}

// New creates a new storage backend instance.
func New() types.Backend {
	return &storageBackend{
		backend: map[string]*internalItem{},
		lock:    &sync.RWMutex{},
	}
}

// Configure configures the storage backend.
func (b *storageBackend) Configure(settings map[string]interface{}, logger hclog.Logger) error {
	b.logger = logger
	return nil
}

func (b *storageBackend) Read(ctx context.Context, key string) (types.SamplingReader, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *storageBackend) Write(ctx context.Context, key string, track *webrtc.TrackRemote) (types.WriterDone, error) {

	opLogger := b.logger.With("key", key)

	b.lock.Lock()
	var item *internalItem
	if v, ok := b.backend[key]; !ok {
		item = &internalItem{}
		b.backend[key] = item
	} else {
		item = v
	}
	b.lock.Unlock()

	codec := track.Codec()
	switch codec.MimeType {
	case webrtc.MimeTypeOpus:
		opLogger.Info("Handling audio track", "mime-type", codec.MimeType)
		item.audioMimeType = codec.MimeType
		item.audioBuf = bytes.NewBuffer([]byte{})
		return recordAudioTrackOpus(ctx, item.videoBuf, track)
	case webrtc.MimeTypeH264:
		opLogger.Info("Handling video track", "mime-type", codec.MimeType)
		item.videoMimeType = codec.MimeType
		item.videoBuf = bytes.NewBuffer([]byte{})
		return recordVideoTrackH264(ctx, item.videoBuf, track)
	case webrtc.MimeTypeVP8:
		opLogger.Info("Handling video track", "mime-type", codec.MimeType)
		item.videoMimeType = codec.MimeType
		item.videoBuf = bytes.NewBuffer([]byte{})
		return recordVideoTrackVP8(ctx, item.videoBuf, track)
	}

	opLogger.Error("Unsupported mime type", "mime-type", codec.MimeType)
	return nil, fmt.Errorf("unsupported: %s", codec.MimeType)
}

// Consider moving these out into a shared library at some point.

func recordTrack(ctx context.Context, writer media.Writer, track *webrtc.TrackRemote) types.WriterDone {
	d := newDone()
	go func() {
		for {
			select {
			case <-ctx.Done():
				writer.Close()
				d.succeeded()
				return
			default:
				rtpPacket, _, err := track.ReadRTP()
				if err != nil {
					writer.Close()
					d.failed(err)
					return
				}
				if err := writer.WriteRTP(rtpPacket); err != nil {
					writer.Close()
					d.failed(err)
					return
				}
			}
		}
	}()
	return d
}

func recordAudioTrackOpus(ctx context.Context, out io.Writer, track *webrtc.TrackRemote) (types.WriterDone, error) {
	w, err := oggwriter.NewWith(out, 48000, 2) // TODO: Extract ogg settings
	if err != nil {
		return nil, err
	}
	return recordTrack(ctx, w, track), nil
}

func recordVideoTrackH264(ctx context.Context, out io.Writer, track *webrtc.TrackRemote) (types.WriterDone, error) {
	w := h264writer.NewWith(out)
	return recordTrack(ctx, w, track), nil
}

func recordVideoTrackVP8(ctx context.Context, out io.Writer, track *webrtc.TrackRemote) (types.WriterDone, error) {
	w, err := ivfwriter.NewWith(out)
	if err != nil {
		return nil, err
	}
	return recordTrack(ctx, w, track), nil
}
