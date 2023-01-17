package memory

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/pion/webrtc/v2/pkg/media/ivfreader"
	"github.com/pion/webrtc/v2/pkg/media/oggreader"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/h264reader"
	"github.com/radekg/boos/pkg/storage/types"
)

type mediaContainerReader struct {
	currentIndex int
	bucket       *mediaBucket
	logger       hclog.Logger
}

func (r *mediaContainerReader) NextContainer() (types.MediaContainer, error) {
	if r.currentIndex < len(r.bucket.containers) {
		defer func() {
			r.currentIndex = r.currentIndex + 1
		}()
		return r.bucket.containers[r.currentIndex], nil
	}
	return nil, io.EOF
}

// Consider moving these out into a shared library at some point.

// =======================
// Playback functionality:
// =======================

type readerStatus struct {
	hasData  bool
	mimeType string
	reader   types.SamplingReader
}

func (v *readerStatus) HasData() bool {
	return v.hasData
}

func (v *readerStatus) MimeType() string {
	return v.mimeType
}

func (v *readerStatus) Reader() types.SamplingReader {
	return v.reader
}

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
