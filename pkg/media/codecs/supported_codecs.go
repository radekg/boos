package codecs

import "github.com/pion/webrtc/v3"

const mimeTypeVideoRtx = "video/rtx"
const enableH264 = true

// AudioCodecs returns a list of audio codecs we support.
func AudioCodecs() []webrtc.RTPCodecParameters {
	return []webrtc.RTPCodecParameters{
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeOpus,
				ClockRate:    48000,
				Channels:     2,
				SDPFmtpLine:  "minptime=10;useinbandfec=1",
				RTCPFeedback: nil,
			},
			PayloadType: 111,
		},
	}
}

// VideoCodecs returns a list of audio codecs we support.
func VideoCodecs() []webrtc.RTPCodecParameters {

	videoRTCPFeedback := []webrtc.RTCPFeedback{
		{Type: "goog-remb", Parameter: ""},
		{Type: "ccm", Parameter: "fir"},
		{Type: "nack", Parameter: ""},
		{Type: "nack", Parameter: "pli"},
	}

	codecs := []webrtc.RTPCodecParameters{}

	if enableH264 {

		// One step forward, registering H264 codecs first makes the H264 work super fine in Firefox.
		// I think this is somehow related to AddTransceiverFromKind() on peer connection
		// which uses internal mediaEngine codecs[0].
		// Recording from Safari is broken, the video is malformed but it does work from Firefox...
		// This requires some iterations but it will probably work when configured correctly.

		videoRTCPH264Feedback := []webrtc.RTCPFeedback{
			{Type: "goog-remb", Parameter: ""},
			{Type: "ccm", Parameter: "fir"},
			{Type: "nack", Parameter: ""},
			{Type: "nack", Parameter: "pli"},
			{Type: "transport-cc", Parameter: ""},
		}

		codecs = append(codecs, []webrtc.RTPCodecParameters{
			{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:     webrtc.MimeTypeH264,
					ClockRate:    90000,
					Channels:     0,
					SDPFmtpLine:  "profile-level-id=42e01f;level-asymmetry-allowed=1;packetization-mode=1",
					RTCPFeedback: videoRTCPH264Feedback,
				},
				PayloadType: 126,
			},
			{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:     mimeTypeVideoRtx,
					ClockRate:    90000,
					Channels:     0,
					SDPFmtpLine:  "apt=126",
					RTCPFeedback: nil,
				},
				PayloadType: 127,
			},
			/*
				{
					RTPCodecCapability: webrtc.RTPCodecCapability{
						MimeType:     webrtc.MimeTypeH264,
						ClockRate:    90000,
						Channels:     0,
						SDPFmtpLine:  "profile-level-id=42e01f;level-asymmetry-allowed=1;packetization-mode=1",
						RTCPFeedback: videoRTCPH264Feedback,
					},
					PayloadType: 98,
				},
				{
					RTPCodecCapability: webrtc.RTPCodecCapability{
						MimeType:     mimeTypeVideoRtx,
						ClockRate:    90000,
						Channels:     0,
						SDPFmtpLine:  "apt=98",
						RTCPFeedback: nil,
					},
					PayloadType: 99,
				},*/
		}...)
	}

	codecs = append(codecs, []webrtc.RTPCodecParameters{
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeVP8,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "max-fs=12288;max-fr=30",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 96,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     "video/rtx",
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=96",
				RTCPFeedback: nil,
			},
			PayloadType: 97,
		},
	}...)

	return codecs
}
