package memory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/pion/webrtc/v2/pkg/media/h264writer"
	"github.com/pion/webrtc/v2/pkg/media/ivfreader"
	"github.com/pion/webrtc/v2/pkg/media/oggreader"
	"github.com/pion/webrtc/v2/pkg/media/oggwriter"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/h264reader"
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

func (impl *storageBackend) AudioMimeType(key string) (string, bool, bool) {
	impl.lock.RLock()
	defer impl.lock.RUnlock()
	item, ok := impl.backend[key]
	if !ok {
		return "", false, ok
	}
	if item.audioBuf == nil {
		return "", false, true
	}
	return item.audioMimeType, true, true
}

func (impl *storageBackend) VideoMimeType(key string) (string, bool, bool) {
	impl.lock.RLock()
	defer impl.lock.RUnlock()
	item, ok := impl.backend[key]
	if !ok {
		return "", false, ok
	}
	if item.videoBuf == nil {
		return "", false, true
	}
	return item.videoMimeType, true, true
}

// Read reads stored media from this storage backend.
func (b *storageBackend) Read(ctx context.Context, key, codecType string) (types.SamplingReader, error) {
	b.lock.RLock()
	item, ok := b.backend[key]
	b.lock.RUnlock()
	if !ok {
		return nil, fmt.Errorf("not found: key %s", key)
	}
	if item.audioMimeType == codecType {
		switch codecType {
		case webrtc.MimeTypeOpus:
			return newOpusSamplingReader(bytes.NewReader(item.audioBuf.Bytes()), b.logger.Named("ogg-reader"))
		}
	} else if item.videoMimeType == codecType {
		switch codecType {
		case webrtc.MimeTypeH264:
			return newH264SamplingReader(item.videoBuf, b.logger.Named("h264-reader"))
		case webrtc.MimeTypeVP8:
			return newVP8SamplingReader(item.videoBuf, b.logger.Named("vp8-reader"))
		}
	}
	return nil, fmt.Errorf("not found: codec %s for key %s", codecType, key)
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

// =======================
// Playback functionality:
// =======================

type sampleWithTimingHint struct {
	sample     media.Sample
	timingHint time.Duration
}

func (v *sampleWithTimingHint) Sample() media.Sample {
	return v.sample
}

func (v *sampleWithTimingHint) TimingHint() time.Duration {
	return v.timingHint
}

// Reading Opus:
var (
	oggPageDuration = time.Millisecond * 20
)

func newOpusSamplingReader(source io.ReadSeeker, logger hclog.Logger) (types.SamplingReader, error) {
	ogg, _, oggErr := oggreader.NewWith(source)
	if oggErr != nil {
		return nil, fmt.Errorf("Failed creating new Opus reader: %v", oggErr)
	}
	return &oggSamplingReader{r: ogg, l: logger}, nil
}

type oggSamplingReader struct {
	r           *oggreader.OggReader
	l           hclog.Logger
	lastGranule uint64
}

func (impl *oggSamplingReader) NextSample() (types.SampleWithTimingHint, error) {
	pageData, pageHeader, oggErr := impl.r.ParseNextPage()
	if oggErr != nil {
		if errors.Is(oggErr, io.EOF) {
			return nil, io.EOF // always make sure we return EOF when we want EOF
		}
		return nil, oggErr
	}
	// The amount of samples is the difference between the last and current timestamp
	sampleCount := float64(pageHeader.GranulePosition - impl.lastGranule)
	impl.lastGranule = pageHeader.GranulePosition
	sampleDuration := time.Duration((sampleCount/48000)*1000) * time.Millisecond
	return &sampleWithTimingHint{
		sample: media.Sample{
			Data:     pageData,
			Duration: sampleDuration,
		},
		timingHint: oggPageDuration,
	}, nil
}

// Reading H264
func newH264SamplingReader(source io.Reader, logger hclog.Logger) (types.SamplingReader, error) {
	r, err := h264reader.NewReader(source)
	if err != nil {
		return nil, fmt.Errorf("Failed creating new H264 reader: %v", err)
	}
	return &h264SamplingReader{r: r, l: logger}, nil
}

type h264SamplingReader struct {
	r *h264reader.H264Reader
	l hclog.Logger
}

func (impl *h264SamplingReader) NextSample() (types.SampleWithTimingHint, error) {
	nal, nalErr := impl.r.NextNAL()
	if nalErr != nil {
		if errors.Is(nalErr, io.EOF) {
			return nil, io.EOF // always make sure we return EOF when we want EOF
		}
		return nil, nalErr
	}
	return &sampleWithTimingHint{
		sample: media.Sample{
			Data:     nal.Data,
			Duration: time.Second,
		},
		timingHint: time.Millisecond * 33, // 30 fps
	}, nil
}

// Reading VP8
func newVP8SamplingReader(source io.Reader, logger hclog.Logger) (types.SamplingReader, error) {
	r, h, err := ivfreader.NewWith(source)
	if err != nil {
		return nil, fmt.Errorf("Failed creating new VP8 reader: %v", err)
	}
	return &vp8SamplingReader{r: r, h: h, l: logger}, nil
}

type vp8SamplingReader struct {
	r      *ivfreader.IVFReader
	h      *ivfreader.IVFFileHeader
	l      hclog.Logger
	lastTs uint64
}

func (impl *vp8SamplingReader) NextSample() (types.SampleWithTimingHint, error) {
	frame, header, ivfErr := impl.r.ParseNextFrame()
	if ivfErr != nil {
		if errors.Is(ivfErr, io.EOF) {
			return nil, io.EOF // always make sure we return EOF when we want EOF
		}
		return nil, ivfErr
	}
	var diffMs int64
	if impl.lastTs > 0 {
		diff := header.Timestamp - impl.lastTs
		diffMs = int64(diff)
	}
	impl.lastTs = header.Timestamp
	return &sampleWithTimingHint{
		sample: media.Sample{
			Data:            frame,
			PacketTimestamp: uint32(header.Timestamp),
			Duration:        time.Second,
		},
		timingHint: time.Duration(diffMs/100) * time.Millisecond,
	}, nil
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
