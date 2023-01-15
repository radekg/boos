package memory

import (
	"bytes"
	"context"
	"errors"
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

// Read reads stored media from this storage backend.
func (b *storageBackend) Read(ctx context.Context, key string) (types.SamplingReader, error) {
	return nil, fmt.Errorf("not implemented")
}

// Write writes stored media to this storage backend.
func (b *storageBackend) Write(ctx context.Context, key string, track *webrtc.TrackRemote) (types.WriterStatus, error) {

	codec := track.Codec()

	opLogger := b.logger.With("key", key,
		"codec", codec.MimeType,
		"kind", track.Kind(),
		"payload-type", track.PayloadType())

	b.lock.Lock()
	var item *internalItem
	if v, ok := b.backend[key]; !ok {
		item = &internalItem{}
		b.backend[key] = item
	} else {
		item = v
	}
	b.lock.Unlock()

	switch codec.MimeType {
	case webrtc.MimeTypeOpus:
		opLogger.Info("Handling audio track", "mime-type", codec.MimeType)
		item.audioMimeType = codec.MimeType
		item.audioBuf = bytes.NewBuffer([]byte{})
		return recordAudioTrackOpus(ctx, &RecorderSettings{
			Writer: item.audioBuf,
			Track:  track,
			Logger: opLogger,
		})
	case webrtc.MimeTypeH264:
		opLogger.Info("Handling video track", "mime-type", codec.MimeType)
		item.videoMimeType = codec.MimeType
		item.videoBuf = bytes.NewBuffer([]byte{})
		return recordVideoTrackH264(ctx, &RecorderSettings{
			Writer: item.videoBuf,
			Track:  track,
			Logger: opLogger,
		})
	case webrtc.MimeTypeVP8:
		opLogger.Info("Handling video track", "mime-type", codec.MimeType)
		item.videoMimeType = codec.MimeType
		item.videoBuf = bytes.NewBuffer([]byte{})
		return recordVideoTrackVP8(ctx, &RecorderSettings{
			Writer: item.videoBuf,
			Track:  track,
			Logger: opLogger,
		})
	}

	opLogger.Error("Unsupported mime type", "mime-type", codec.MimeType)
	return nil, fmt.Errorf("unsupported: %s", codec.MimeType)
}

// Consider moving these out into a shared library at some point.

// RecorderSettings contains arguments required by the recorder operation.
type RecorderSettings struct {
	Writer io.Writer
	Track  *webrtc.TrackRemote
	Logger hclog.Logger
}

func recordTrack(ctx context.Context, mediaWriter media.Writer, settings *RecorderSettings) types.WriterStatus {
	d := newStatus()
	go func() {
		for {
			select {
			case <-ctx.Done():
				settings.Logger.Info("Track finished, reporting success")
				mediaWriter.Close()
				d.succeeded()
				return
			default:
				rtpPacket, _, err := settings.Track.ReadRTP()
				if errors.Is(err, io.EOF) {
					continue
				}
				if err != nil {
					settings.Logger.Error("Failed reading RTP for track", "reason", err)
					mediaWriter.Close()
					d.failed(err)
					return
				}
				if err := mediaWriter.WriteRTP(rtpPacket); err != nil {
					settings.Logger.Error("Failed writing RTP to media writer for track", "reason", err)
					mediaWriter.Close()
					d.failed(err)
					return
				}
			}
		}
	}()
	return d
}

func recordAudioTrackOpus(ctx context.Context, settings *RecorderSettings) (types.WriterStatus, error) {
	mediaWriter, err := oggwriter.NewWith(settings.Writer, 48000, 2) // TODO: Extract ogg settings
	if err != nil {
		return nil, err
	}
	return recordTrack(ctx, mediaWriter, settings), nil
}

func recordVideoTrackH264(ctx context.Context, settings *RecorderSettings) (types.WriterStatus, error) {
	return recordTrack(ctx, h264writer.NewWith(settings.Writer), settings), nil
}

func recordVideoTrackVP8(ctx context.Context, settings *RecorderSettings) (types.WriterStatus, error) {
	mediaWriter, err := ivfwriter.NewWith(settings.Writer)
	if err != nil {
		return nil, err
	}
	return recordTrack(ctx, mediaWriter, settings), nil
}
