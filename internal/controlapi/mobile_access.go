package controlapi

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"open-mihomo-gateway/internal/macosnetwork"
)

// mobileAccessSnapshot is kept behind mobileMu because toggling access changes
// the Host and Origin allowlists while requests may be in flight.
func (s *Server) mobileAccessSnapshot() (interfaceName, address, baseURL string) {
	s.mobileMu.RLock()
	defer s.mobileMu.RUnlock()
	return s.lanInterface, s.lanAddr, s.lanBaseURL
}

func (s *Server) mobileAccessResponse() MobileAccessResponse {
	interfaceName, address, baseURL := s.mobileAccessSnapshot()
	return MobileAccessResponse{
		SchemaVersion: SchemaVersion,
		Enabled:       baseURL != "",
		Interface:     interfaceName,
		Address:       address,
		BaseURL:       baseURL,
	}
}

// setMobileAccess applies the change without interrupting the loopback
// listener. It persists before exposing the new listener, so a later launchd
// restart restores the user's explicit choice.
func (s *Server) setMobileAccess(settings MobileAccessSettings) error {
	settings.Interface = strings.TrimSpace(settings.Interface)
	if settings.Enabled && settings.Interface == "" {
		return fmt.Errorf("an interface is required when enabling mobile access")
	}

	s.mobileMu.Lock()
	defer s.mobileMu.Unlock()

	if !settings.Enabled {
		if err := s.store.SaveMobileAccess(settings); err != nil {
			return fmt.Errorf("save mobile access settings: %w", err)
		}
		listener := s.mobileListener
		s.mobileListener = nil
		s.lanAddr = ""
		s.lanBaseURL = ""
		s.lanInterface = ""
		if listener != nil {
			_ = listener.Close()
		}
		return nil
	}

	if s.httpServer == nil {
		return fmt.Errorf("control service is not ready to update mobile access")
	}
	ip, err := macosnetwork.InterfaceIPv4(settings.Interface)
	if err != nil {
		return fmt.Errorf("resolve mobile access interface: %w", err)
	}
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		return fmt.Errorf("read control API port: %w", err)
	}
	address := net.JoinHostPort(ip, port)
	baseURL := "http://" + address
	if address == s.lanAddr && s.mobileListener != nil {
		if err := s.store.SaveMobileAccess(settings); err != nil {
			return fmt.Errorf("save mobile access settings: %w", err)
		}
		s.lanInterface = settings.Interface
		return nil
	}

	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return fmt.Errorf("listen for mobile access on %s: %w", address, err)
	}
	if err := s.store.SaveMobileAccess(settings); err != nil {
		_ = listener.Close()
		return fmt.Errorf("save mobile access settings: %w", err)
	}
	oldListener := s.mobileListener
	s.mobileListener = listener
	s.lanAddr = address
	s.lanBaseURL = baseURL
	s.lanInterface = settings.Interface
	if oldListener != nil {
		_ = oldListener.Close()
	}
	go s.serveMobileListener(s.httpServer, listener)
	return nil
}

func (s *Server) serveMobileListener(httpServer *http.Server, listener net.Listener) {
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
		// A disabled listener is expected to return an error from Accept; avoid
		// making that normal switch action look like a control-plane outage.
		log.Printf("control API: mobile listener stopped: %v", err)
	}
}
