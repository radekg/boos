package backend

import (
	"fmt"
	"sync"

	guuid "github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-hclog"
	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v3"
)

// PeerClientType represents the types of signal messages
type PeerClientType int

const (
	// PctUndecided - undetermined
	PctUndecided = PeerClientType(iota)

	// PctRecord - recording client
	PctRecord = PeerClientType(iota)

	// PctPlayback - playback client
	PctPlayback = PeerClientType(iota)
)

// PeerClient represents a server-side client used as a peer to the browser client.
type PeerClient struct {
	id string

	decidedType PeerClientType

	pc *webrtc.PeerConnection
	ws *websocket.Conn

	browserSD string
	serverSD  string

	sdParsed sdp.SessionDescription

	services *WebRTCService

	logger hclog.Logger

	closeCh chan struct{}

	wg    sync.WaitGroup
	mutex sync.Mutex
}

// CreateNewPeerClient creates a new server peer client.
func CreateNewPeerClient(conn *websocket.Conn, services *WebRTCService, logger hclog.Logger) (*PeerClient, error) {
	clientID := guuid.New().String()
	client := PeerClient{
		id:          clientID,
		decidedType: PctUndecided,
		ws:          conn,
		logger:      logger.Named(fmt.Sprintf("peer-%s", clientID)).With("client-id", clientID),
		services:    services,
		closeCh:     make(chan struct{}),
	}
	client.logger.Info("Server Peer Client created")
	go client.eventLoop()
	return &client, nil
}

// IsClosed checks to see if this client has been shutdown
func (c *PeerClient) closed() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	select {
	case _, ok := <-c.closeCh:
		if !ok {
			return true
		}
	default:
	}
	return false
}

// Close - closes a client's peer and signal connections.
func (c *PeerClient) Close() {
	if c.closed() {
		c.logger.Warn("Client already closed")
		return
	}
	c.mutex.Lock()
	close(c.closeCh)
	c.mutex.Unlock()
	c.ws.Close()
	if c.pc != nil {
		if err := c.pc.Close(); err != nil {
			c.logger.Warn("Client peer connection closed with error", err)
		} else {
			c.logger.Info("Client peer connection closed without error")
		}
	}
	c.logger.Info("Client waiting for event loop to finish")
	c.wg.Wait()
	c.logger.Info("Client closed")
}

func (c *PeerClient) eventLoop() {
	c.wg.Add(1)
	defer func() {
		c.logger.Info("Server Peer Client is exiting event loop")
		c.wg.Done()
	}()

	var ev SignalMessage
	var err error

	for {
		if c.closed() {
			return
		}

		err = c.ws.ReadJSON(&ev)
		if err != nil {
			c.logger.Error("Client failed reading JSON data from WebSocket, closing...", "reason", err)
			go c.Close()
			return
		}

		err = ev.Unmarshal()
		if err != nil {
			c.logger.Error("Client failed receiving signal event", "reason", err)
			continue
		}

		c.logger.Info("Client received an event", "event", ev.Op)

		switch ev.id {
		case SmRecord:
			if c.decidedType != PctUndecided {
				c.sendError("Peer client is already either recording or playing. Please disconnect and try again.")
				continue
			}
			c.decidedType = PctRecord
			c.browserSD = ev.Data
			go func() {
				c.wg.Add(1)
				defer c.wg.Done()
				err = c.services.CreateRecordingConnection(c)
				if err != nil {
					c.logger.Error("Client recording error", "reason", err)
				}
			}()

		case SmPlay:
			if c.decidedType != PctUndecided {
				c.sendError("Peer client is already either recording or playing. Please disconnect and try again.")
				continue
			}
			// TODO: use configured storage here
			// if !c.services.HasRecordings() {
			// 	c.sendError("There are no recorded videos to playback. Please record a video first.")
			// 	continue
			// }
			c.decidedType = PctPlayback
			c.browserSD = ev.Data
			go func() {
				c.wg.Add(1)
				defer c.wg.Done()
				err = c.services.CreatePlaybackConnection(c)
				if err != nil {
					c.logger.Error("Client playback error", "reason", err)
				}
			}()
		}
	}
}

// CloseChan returns a channel that gets closed when this peer client is closed.
func (c *PeerClient) CloseChan() <-chan struct{} {
	return c.closeCh
}

// startServerSession - Completes the session initiation with the client.
func (c *PeerClient) startServerSession() error {

	offer := webrtc.SessionDescription{}
	Decode(c.browserSD, &offer)

	// Some browser codec mappings might not match what
	// pion has. The work around is to pull the payload type
	// from the browser session description and modify the
	// streaming rtp packets accordingly. See this issue for
	// more details: https://github.com/pion/webrtc/issues/716
	err := c.sdParsed.Unmarshal([]byte(offer.SDP))
	if err != nil {
		return err
	}
	/*
		TODO: fix
			codec := sdp.Codec{
				Name: c.services.vc.MimeType,
			}
			c.pt, err = c.sdParsed.GetPayloadTypeForCodec(codec)
			if err != nil {
				return err
			}
	*/

	// ---

	// Set the remote session description
	err = c.pc.SetRemoteDescription(offer)
	if err != nil {
		c.logger.Error("Failed setting remote description", "reason", err)
		return err
	}

	// Create answer
	answer, err := c.pc.CreateAnswer(nil)
	if err != nil {
		c.logger.Error("Failed creating an answer", "reason", err)
		return err
	}

	// Starts the UDP listeners
	err = c.pc.SetLocalDescription(answer)
	if err != nil {
		c.logger.Error("Failed setting local description", "reason", err)
		return err
	}

	// Send back the answer (this peer's session description) in base64 to the browser client.
	// Note modifications may be made to account for known issues. See ModServerSessionDescription()
	// for more details.
	c.serverSD = Encode(ModAnswer(&answer))

	msg := SignalMessage{}
	msg.id = SmAnswer
	msg.Data = c.serverSD
	msg.Marshal()

	err = c.ws.WriteJSON(&msg)
	if err != nil {
		return err
	}
	return nil
}

func (c *PeerClient) sendError(errMsg string) error {
	c.logger.Error("Client reporting error to a peer", "error", errMsg)

	msg := SignalMessage{}
	msg.id = SmError
	msg.Data = errMsg
	msg.Marshal()

	err := c.ws.WriteJSON(&msg)
	if err != nil {
		return err
	}

	return nil
}
