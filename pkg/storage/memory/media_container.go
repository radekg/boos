package memory

import (
	"bytes"
	"fmt"

	"github.com/hashicorp/go-hclog"
	"github.com/pion/webrtc/v3"
	"github.com/radekg/boos/pkg/storage/types"
)

type mediaContainer struct {
	audioMimeType string
	videoMimeType string
	audioBuf      *bytes.Buffer
	videoBuf      *bytes.Buffer
}

func (c *mediaContainer) Audio(key string) (types.ReaderStatus, error) {
	result := &readerStatus{}
	result.hasData = c.audioBuf != nil
	if result.hasData {
		result.mimeType = c.audioMimeType
		switch result.mimeType {
		case webrtc.MimeTypeOpus:
			reader, err := newOpusSamplingReader(bytes.NewReader(c.audioBuf.Bytes()), hclog.Default().Named("ogg-reader"))
			if err != nil {
				return nil, err
			}
			result.reader = reader
			return result, nil
		default:
			return nil, fmt.Errorf("Invalid mime type: audio '%s' for key '%s'", c.audioMimeType, key)
		}
	}
	return result, nil
}

func (c *mediaContainer) Video(key string) (types.ReaderStatus, error) {
	result := &readerStatus{}
	result.hasData = c.videoBuf != nil
	if result.hasData {
		result.mimeType = c.videoMimeType
		switch result.mimeType {
		case webrtc.MimeTypeH264:
			reader, err := newH264SamplingReader(bytes.NewBuffer(c.videoBuf.Bytes()), hclog.Default().Named("h264-reader"))
			if err != nil {
				return nil, err
			}
			result.reader = reader
			return result, nil
		case webrtc.MimeTypeVP8:
			reader, err := newVP8SamplingReader(bytes.NewBuffer(c.videoBuf.Bytes()), hclog.Default().Named("vp8-reader"))
			if err != nil {
				return nil, err
			}
			result.reader = reader
			return result, nil
		default:
			return nil, fmt.Errorf("Invalid mime type: audio '%s' for key '%s'", c.audioMimeType, key)
		}
	}
	return result, nil
}
