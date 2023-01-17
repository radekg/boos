package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/radekg/boos/configs"
	"golang.org/x/image/vp8"

	"github.com/radekg/boos/pkg/media/codecs"
	"github.com/radekg/boos/pkg/storage/types"
)

// WebRTCService server implementation
type WebRTCService struct {
	api         *webrtc.API
	config      webrtc.Configuration
	mediaEngine *webrtc.MediaEngine

	storage types.Backend

	logger hclog.Logger
}

// CreateNewWebRTCService creates a new webrtc server instance
func CreateNewWebRTCService(webRTCConfig *configs.WebRTCConfig, storage types.Backend, logger hclog.Logger) (*WebRTCService, error) {

	svc := WebRTCService{
		logger:      logger,
		mediaEngine: &webrtc.MediaEngine{},
		storage:     storage,
	}

	// Create a InterceptorRegistry. This is the user configurable RTP/RTCP Pipeline.
	// This provides NACKs, RTCP Reports and other features. If you use `webrtc.NewPeerConnection`
	// this is enabled by default. If you are manually managing You MUST create a InterceptorRegistry
	// for each PeerConnection.
	i := &interceptor.Registry{}

	// Use the default set of Interceptors
	if err := webrtc.RegisterDefaultInterceptors(svc.mediaEngine, i); err != nil {
		svc.logger.Error("Failed registering an interceptor registry", "reason", err)
		return nil, err
	}

	// Select the exact codecs we're prepared to handle. Otherwise peers may
	// negotiate something that we're not prepared to handle.
	// Preferably, we should be able to simply register the default set of codecs
	// but we need more logic around recording handling.
	// For example, Safari will negotiate H264.

	// if err := svc.mediaEngine.RegisterDefaultCodecs(); err != nil {
	//	svc.logger.Error("Failed registering default codecs", "reason", err)
	//	return nil, err
	// }

	audioCodecs := codecs.AudioCodecs()
	for _, c := range audioCodecs {
		if err := svc.mediaEngine.RegisterCodec(c, webrtc.RTPCodecTypeAudio); err != nil {
			svc.logger.Error("Failed registering audio codec", "reason", err)
			return nil, err
		}
	}
	videoCodecs := codecs.VideoCodecs(webRTCConfig.H264)
	for _, c := range videoCodecs {
		if err := svc.mediaEngine.RegisterCodec(c, webrtc.RTPCodecTypeVideo); err != nil {
			svc.logger.Error("Failed registering audio codec", "reason", err)
			return nil, err
		}
	}

	svc.api = webrtc.NewAPI(webrtc.WithMediaEngine(svc.mediaEngine), webrtc.WithInterceptorRegistry(i))

	svc.config = webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: webRTCConfig.ICEServers,
			},
		},
	}

	svc.logger.Info("WebRTC services started with default codecs")
	return &svc, nil
}

// CreateRecordingConnection creates a new webrtc peer connection on the server for recording and streaming playback.
func (svc *WebRTCService) CreateRecordingConnection(client *PeerClient) error {

	ctxDone, ctxDoneCancelFunc := context.WithCancel(context.Background())

	opLogger := svc.logger.With("operation", "recording", "client-id", client.id)

	var err error

	// Create a new peer connection
	client.pc, err = svc.api.NewPeerConnection(svc.config)
	if err != nil {
		opLogger.Error("Failed creating new peer connection for client", "reason", err)
		ctxDoneCancelFunc()
		return err
	}

	// Allow us to receive 1 audio track, and 1 video track
	if _, err = client.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
		opLogger.Error("Failed adding transceiver from audio kind", "reason", err)
		ctxDoneCancelFunc()
		return err
	} else if _, err = client.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo); err != nil {
		opLogger.Error("Failed adding transceiver from video kind", "reason", err)
		ctxDoneCancelFunc()
		return err
	}

	recordingID := "hardcoded" // TODO: add support
	mediaContainerWriter, err := svc.storage.MediaContainerWriterForBucket(recordingID)
	if err != nil {
		opLogger.Error("Failed creating media container writer", "reason", err)
		ctxDoneCancelFunc()
		return err
	}

	// Handler - Process audio/video as it is received
	client.pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {

		onTrackLogger := opLogger.With("codec", track.Codec().MimeType,
			"kind", track.Kind(),
			"payload-type", track.PayloadType())

		onTrackLogger.Info("Client track available")

		// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
		go func() {
			ticker := time.NewTicker(time.Second * 3)
			for range ticker.C {
				select {
				case <-ctxDone.Done():
					onTrackLogger.Info("OnTrack PLI regular, client stopped")
					return
				default:
					err := client.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}})
					if err != nil {
						onTrackLogger.Error("OnTrack PLI exiting due to an error", "reason", err)
						return
					}
				}
			}
		}()

		status, err := mediaContainerWriter.Write(ctxDone, recordingID, track)
		if err != nil {
			onTrackLogger.Error("Error configuring track recording", "reason", err)
			// TODO: what's the best thing to do here...?
		} else {
			go func() {
				select {
				case <-status.Success():
					onTrackLogger.Info("Track recorded successfully")
				case err := <-status.Fail():
					onTrackLogger.Error("Track recording failed", "reason", err)
				}
			}()
		}

	})

	// Handler - Detect connects, disconnects & closures
	client.pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		if connectionState == webrtc.ICEConnectionStateConnected {
			opLogger.Info("Client connected to webrtc services as peer", "connection-state", connectionState.String())
		} else if connectionState == webrtc.ICEConnectionStateFailed ||
			connectionState == webrtc.ICEConnectionStateDisconnected ||
			connectionState == webrtc.ICEConnectionStateClosed {
			opLogger.Info("Client disconnected from webrtc services as peer", "connection-state", connectionState.String())
		} else {
			opLogger.Info("Client connection State has changed", "connection-state", connectionState.String())
		}
	})

	go func() {
		<-client.CloseChan()
		opLogger.Info("Client claims closed, stopping any ongoing recording")
		ctxDoneCancelFunc()
	}()

	err = client.startServerSession()
	if err != nil {
		return err
	}

	return nil
}

// CreatePlaybackConnection creates a new webrtc peer connection on the server for recording and streaming playback.
func (svc *WebRTCService) CreatePlaybackConnection(client *PeerClient) error {

	iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())

	opLogger := svc.logger.With("operation", "playback", "client-id", client.id)

	var err error
	client.pc, err = svc.api.NewPeerConnection(svc.config)
	if err != nil {
		opLogger.Error("Failed creating new peer connection for client", "reason", err)
		iceConnectedCtxCancel()
		return err
	}

	recordingID := "hardcoded" // TODO: add support

	if !svc.storage.ContainsBucket(recordingID) {
		opLogger.Error("No recording for key", "key", recordingID)
		iceConnectedCtxCancel()
		return fmt.Errorf("no recording: %s", recordingID)
	}

	mediaReader, err := svc.storage.MediaContainerReaderForBucket(recordingID)
	if err != nil {
		opLogger.Error("Failed creating media container reader for key", "key", recordingID, "reason", err)
		iceConnectedCtxCancel()
		return fmt.Errorf("no reader for media: %s", recordingID)
	}

	mediaContainer, err := mediaReader.NextContainer()
	if mediaContainer == nil || errors.Is(err, io.EOF) {
		opLogger.Error("No media to deliver for key", "key", recordingID)
		iceConnectedCtxCancel()
		return fmt.Errorf("no reader for media: %s", recordingID)
	}
	if err != nil {
		opLogger.Error("Error fetching first media container", "key", recordingID, "reason", err)
		iceConnectedCtxCancel()
		return fmt.Errorf("no reader for media: %s", recordingID)
	}

	audioStatus, err := mediaContainer.Audio(recordingID)
	if err != nil {
		opLogger.Error("Failed checking audio status", "reason", err)
		iceConnectedCtxCancel()
		return fmt.Errorf("recording error: audio %s", recordingID)
	}

	videoStatus, err := mediaContainer.Video(recordingID)
	if err != nil {
		opLogger.Error("Failed checking video status", "reason", err)
		iceConnectedCtxCancel()
		return fmt.Errorf("recording error: video %s", recordingID)
	}

	if audioStatus.HasData() {
		// Create a audio track
		audioTrack, audioTrackErr := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: audioStatus.MimeType()}, "audio", "pion")
		if audioTrackErr != nil {
			opLogger.Error("Failed creating webrtc audio track", "reason", err)
			// TODO: what do I do with this...?
		}

		rtpSender, audioTrackErr := client.pc.AddTrack(audioTrack)
		if audioTrackErr != nil {
			opLogger.Error("Failed adding audio track to webrtc peer client", "reason", err)
			// TODO: what do I do with this...?
		}

		// Read incoming RTCP packets
		// Before these packets are returned they are processed by interceptors. For things
		// like NACK this needs to be called.
		go func() {
			rtcpBuf := make([]byte, 1500)
			for {
				if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
					return
				}
			}
		}()

		go func() {
			audioSamplingReader := audioStatus.Reader()
			// Wait for connection established
			<-iceConnectedCtx.Done()
			// Send frames:
			for {
				sampleAndTimingHint, err := audioSamplingReader.NextSample()
				if err != nil {
					if errors.Is(err, io.EOF) {
						opLogger.Info("All audio frames parsed and sent")
						break
					}
					opLogger.Error("Failed fetching next audio sample", "reason", err)
					break // TODO: what do I do with the error?
				}
				if sampleWriteError := audioTrack.WriteSample(sampleAndTimingHint.Sample()); sampleWriteError != nil {
					opLogger.Error("Failed writing audio media sample", "reason", sampleWriteError)
					break // TODO: what do I do with the error?
				}
				if sampleAndTimingHint.TimingHint() > 0 {
					// Okay, what I'm seeing ts that various input devices send various frame rates.
					// What's even more interesting, for an Apple Screen camera, I see variable number of frames
					// in every second of a stream.
					// Because of that, I assume that the only correct way to recreate the pace of the video
					// is to calculate the exact difference between frames in milliseconds.
					// Basically, there's no such thing as fps, it's maximum fps.
					//
					// I can be a bit smarter about these timings. I can calculate how long did it take me
					// to parse data in sampling reader NextSample() and deduct this value from the
					// timing hint so that I arrive at more accurate number.
					<-time.After(sampleAndTimingHint.TimingHint())
				}
			}
		}()
	} // end audio delivery

	if videoStatus.HasData() {
		// Create a video track
		videoTrack, videoTrackErr := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: videoStatus.MimeType()}, "video", "pion")
		if videoTrackErr != nil {
			opLogger.Error("Failed creating webrtc video track", "reason", err)
			// TODO: what do I do with this...?
		}
		rtpSender, videoTrackErr := client.pc.AddTrack(videoTrack)
		if videoTrackErr != nil {
			opLogger.Error("Failed adding video track to webrtc peer client", "reason", err)
			// TODO: what do I do with this...?
		}

		// Read incoming RTCP packets
		// Before these packets are returned they are processed by interceptors. For things
		// like NACK this needs to be called.
		go func() {
			rtcpBuf := make([]byte, 1500)
			for {
				if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
					return
				}
			}
		}()

		go func() {
			samplingReader := videoStatus.Reader()
			// Wait for connection established
			<-iceConnectedCtx.Done()
			// Send frames:
			for {
				sampleAndTimingHint, err := samplingReader.NextSample()
				if err != nil {
					if errors.Is(err, io.EOF) {
						opLogger.Info("All video frames parsed and sent")
						break
					}
					opLogger.Error("Failed fetching next video sample", "reason", err)
					break // TODO: what do I do with the error?
				}
				if sampleWriteError := videoTrack.WriteSample(sampleAndTimingHint.Sample()); sampleWriteError != nil {
					opLogger.Error("Failed writing video media sample", "reason", sampleWriteError)
					break // TODO: what do I do with the error?
				}
				if sampleAndTimingHint.TimingHint() > 0 {
					// Okay, what I'm seeing ts that various input devices send various frame rates.
					// What's even more interesting, for an Apple Screen camera, I see variable number of frames
					// in every second of a stream.
					// Because of that, I assume that the only correct way to recreate the pace of the video
					// is to calculate the exact difference between frames in milliseconds.
					// Basically, there's no such thing as fps, it's maximum fps.
					//
					// I can be a bit smarter about these timings. I can calculate how long did it take me
					// to parse data in sampling reader NextSample() and deduct this value from the
					// timing hint so that I arrive at more accurate number.
					<-time.After(sampleAndTimingHint.TimingHint())
				}
			}
		}()
	} // end video delivery

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected

	client.pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		svc.logger.Info("ICE connection state has changed", "connection-state", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateConnected {
			iceConnectedCtxCancel()
		}
	})

	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	client.pc.OnConnectionStateChange(func(connectionState webrtc.PeerConnectionState) {
		if connectionState == webrtc.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			svc.logger.Error("Peer Connection has gone to failed", "connection-state", connectionState.String())
			//os.Exit(0)
		} else {
			svc.logger.Info("Peer Connection state has changed", "connection-state", connectionState.String())
		}
	})

	err = client.startServerSession()
	if err != nil {
		return err
	}

	return nil
}

// RTPToString compiles the rtp header fields into a string for logging.
func RTPToString(pkt *rtp.Packet) string {
	return fmt.Sprintf("RTP:{Version:%d Padding:%v Extension:%v Marker:%v PayloadType:%d SequenceNumber:%d Timestamp:%d SSRC:%d CSRC:%v ExtensionProfile:%d PayloadLen:%d}",
		pkt.Version,
		pkt.Padding,
		pkt.Extension,
		pkt.Marker,
		pkt.PayloadType,
		pkt.SequenceNumber,
		pkt.Timestamp,
		pkt.SSRC,
		pkt.CSRC,
		pkt.ExtensionProfile,
		len(pkt.Payload),
	)
}

// VP8FrameHeaderToString compiles a vp8 video frame header fields into a string for logging.
func VP8FrameHeaderToString(fh *vp8.FrameHeader) string {
	return fmt.Sprintf("VP8:{KeyFrame:%v VersionNumber:%d ShowFrame:%v FirstPartitionLen:%d Width:%d Height:%d XScale:%d YScale:%d}",
		fh.KeyFrame,
		fh.VersionNumber,
		fh.ShowFrame,
		fh.FirstPartitionLen,
		fh.Width,
		fh.Height,
		fh.XScale,
		fh.YScale,
	)

}

// VP8FrameHeaderToString compiles a vp8 video frame header fields into a string for logging.
func RTPHeaderToString(fh rtp.Header) string {
	return fmt.Sprintf("VP8:{Timestamp:%d SequenceNumber:%d, CSRC: %v, Extension: %v, ExtensionProfile: %v, Extensions: %v, Marker: %v, Padding: %v, PayloadType: %v, SSRC: %v, Version: %v}",
		fh.Timestamp,
		fh.SequenceNumber,
		fh.CSRC,
		fh.Extension,
		fh.ExtensionProfile,
		fh.Extensions,
		fh.Marker,
		fh.Padding,
		fh.PayloadType,
		fh.SSRC,
		fh.Version,
	)

}

/*
// SaveAsPNG saves the specified image as a png file.
func SaveAsPNG(img *image.YCbCr, fn string) error {
	f, err := os.Create(fn)
	if err != nil {
		return err
	}
	defer f.Close()

	err = png.Encode(f, (*image.YCbCr)(img))
	if err != nil {
		return err
	}

	/// log.Printf("PNG file saved: %s\n", fn)
	return nil
}
*/

// ModAnswer modifies the remote session description to work around known issues.
func ModAnswer(sd *webrtc.SessionDescription) *webrtc.SessionDescription {
	// https://stackoverflow.com/questions/47990094/failed-to-set-remote-video-description-send-parameters-on-native-ios
	sd.SDP = strings.Replace(sd.SDP, "42001f", "42e01f", -1)
	return sd
}
