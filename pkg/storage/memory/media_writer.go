package memory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/hashicorp/go-hclog"
	"github.com/pion/webrtc/v2/pkg/media/h264writer"
	"github.com/pion/webrtc/v2/pkg/media/oggwriter"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/radekg/boos/pkg/media/ivfwriter"
	"github.com/radekg/boos/pkg/storage/types"
)

type mediaContainerWriter struct {
	container *mediaContainer
	logger    hclog.Logger
}

// Write writes stored media to this storage backend.
func (w *mediaContainerWriter) Write(ctx context.Context, key string, track *webrtc.TrackRemote) (types.WriterStatus, error) {

	codec := track.Codec()

	opLogger := w.logger.With("key", key,
		"codec", codec.MimeType,
		"kind", track.Kind(),
		"payload-type", track.PayloadType())

	switch codec.MimeType {
	case webrtc.MimeTypeOpus:
		opLogger.Info("Handling audio track", "mime-type", codec.MimeType)
		w.container.audioMimeType = codec.MimeType
		w.container.audioBuf = bytes.NewBuffer([]byte{})
		return recordAudioTrackOpus(ctx, &RecorderSettings{
			Writer: w.container.audioBuf,
			Track:  track,
			Logger: opLogger,
		})
	case webrtc.MimeTypeH264:
		opLogger.Info("Handling video track", "mime-type", codec.MimeType)
		w.container.videoMimeType = codec.MimeType
		w.container.videoBuf = bytes.NewBuffer([]byte{})
		return recordVideoTrackH264(ctx, &RecorderSettings{
			Writer: w.container.videoBuf,
			Track:  track,
			Logger: opLogger,
		})
	case webrtc.MimeTypeVP8:
		opLogger.Info("Handling video track", "mime-type", codec.MimeType)
		w.container.videoMimeType = codec.MimeType
		w.container.videoBuf = bytes.NewBuffer([]byte{})
		return recordVideoTrackVP8(ctx, &RecorderSettings{
			Writer: w.container.videoBuf,
			Track:  track,
			Logger: opLogger,
		})
	}

	opLogger.Error("Unsupported mime type", "mime-type", codec.MimeType)
	return nil, fmt.Errorf("unsupported: %s", codec.MimeType)
}

// ========================
// Recording functionality:
// ========================

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
