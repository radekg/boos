package backend

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/go-hclog"
	"github.com/radekg/boos/configs"
)

// Server implements a WebRTC signal server used to locate and connect up with peers.
// In this simple case the peer is the server.
type Server struct {
	frontEndConfig *configs.FrontendConfig
	webRTCConfig   *configs.WebRTCConfig
	logger         hclog.Logger
	services       *WebRTCService
}

// ServeListen creates a new frontend server and attempts to listen.
func ServeListen(backEndConfig *configs.BackendConfig,
	frontEndConfig *configs.FrontendConfig,
	webRTCConfig *configs.WebRTCConfig,
	logger hclog.Logger) error {

	services, err := CreateNewWebRTCService(webRTCConfig, logger.Named("webrtc"))
	if err != nil {
		logger.Error("Failed creating WebRTC service", "reason", err)
		return err
	}

	srv := Server{
		frontEndConfig: frontEndConfig,
		logger:         logger,
		services:       services,
		webRTCConfig:   webRTCConfig,
	}

	http.HandleFunc("/ws", srv.wsHandler)

	chanErr := make(chan error, 1)
	go func() {
		err := http.ListenAndServe(backEndConfig.BindAddress, nil)
		if err != nil {
			chanErr <- err
		}
	}()
	select {
	case err := <-chanErr:
		return err
	case <-time.After(time.Millisecond * 500):
		srv.logger.Info("Backend server started and listening on", "backend-bind-address", backEndConfig.BindAddress)
		// we are golden
	}
	return nil
}

func (s *Server) wsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.frontEndConfig.ExternalAddress {
		s.logger.Error("Access to websocket is forbidden",
			"origin", r.Header.Get("Origin"),
			"frontend-external-address", s.frontEndConfig.ExternalAddress)
		http.Error(w, "Origin not allowed", 403)
		return
	}
	conn, err := websocket.Upgrade(w, r, w.Header(), 1024, 1024)
	if err != nil {
		http.Error(w, "could not open websocket connection", http.StatusBadRequest)
	}

	// TODO: keep a map of clients so connections can be managed properly.
	_, err = CreateNewPeerClient(conn, s.services, s.logger)
	if err != nil {
		s.logger.Error("wsHandle error", "reason", err)
	}
}
