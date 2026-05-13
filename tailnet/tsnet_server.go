//go:build linux

package tailnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

type ParentOptions struct {
	Enabled          bool
	Hostname         string
	StateDir         string
	ListenAddr       string
	AuthKey          string
	AuthKeySource    string
	AuthKeyLoadError error
	Tags             []string
	ExpectedTailnet  string
	Handler          http.Handler
	Node             ParentNode
	Logf             func(format string, args ...any)
}

type ParentNode interface {
	Start() error
	Listen(network string, addr string) (net.Listener, error)
	LocalClient() (PeerIdentifier, error)
	Close() error
}

type PeerIdentifier interface {
	IdentifyPeer(ctx context.Context, remoteAddr string) (core.TailnetPeerIdentity, error)
}

type ParentService struct {
	opts       ParentOptions
	node       ParentNode
	httpServer *http.Server
	listener   net.Listener
	mu         sync.RWMutex
	status     core.TailnetParentStatus
}

func NewParentService(opts ParentOptions) *ParentService {
	if strings.TrimSpace(opts.Hostname) == "" {
		opts.Hostname = "aphelion"
	}
	if strings.TrimSpace(opts.ListenAddr) == "" {
		opts.ListenAddr = ":8765"
	}
	if opts.Handler == nil {
		opts.Handler = http.NotFoundHandler()
	}
	return &ParentService{
		opts: opts,
		status: core.TailnetParentStatus{
			Enabled:       opts.Enabled,
			Hostname:      strings.TrimSpace(opts.Hostname),
			StateDir:      strings.TrimSpace(opts.StateDir),
			ListenAddr:    strings.TrimSpace(opts.ListenAddr),
			AuthKeySource: strings.TrimSpace(opts.AuthKeySource),
			Tags:          normalizeList(opts.Tags),
		},
	}
}

func (s *ParentService) Start(ctx context.Context) error {
	if s == nil || !s.opts.Enabled {
		return nil
	}
	s.mu.RLock()
	running := s.status.Running
	s.mu.RUnlock()
	if running {
		return nil
	}
	if strings.TrimSpace(s.opts.StateDir) == "" {
		return s.failStart("state directory is required")
	}
	stateInitialized := parentStateInitialized(s.opts.StateDir)
	if strings.TrimSpace(s.opts.AuthKey) == "" && !stateInitialized {
		if s.opts.AuthKeyLoadError != nil {
			return s.failStart("load auth key: " + s.opts.AuthKeyLoadError.Error())
		}
		return s.failStart("auth key is required for first parent tsnet start")
	}
	if err := os.MkdirAll(s.opts.StateDir, 0o700); err != nil {
		return s.failStart("prepare state directory: " + err.Error())
	}
	node := s.opts.Node
	if node == nil {
		node = newRealParentNode(s.opts)
	}
	if err := node.Start(); err != nil {
		_ = node.Close()
		return s.failStart("start parent tsnet node: " + err.Error())
	}
	identifier, err := node.LocalClient()
	if err != nil {
		_ = node.Close()
		return s.failStart("prepare parent tsnet identity lookup: " + err.Error())
	}
	ln, err := node.Listen("tcp", s.opts.ListenAddr)
	if err != nil {
		_ = node.Close()
		return s.failStart("listen on parent tsnet node: " + err.Error())
	}
	srv := &http.Server{
		Handler:           attachPeerIdentity(s.opts.Handler, identifier),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	s.mu.Lock()
	s.node = node
	s.listener = ln
	s.httpServer = srv
	status := s.status
	status.Enabled = true
	status.Running = true
	status.ListenAddr = ln.Addr().String()
	status.MagicDNSURL = ParentMagicDNSURL(s.opts.Hostname, s.opts.ExpectedTailnet, s.opts.ListenAddr)
	status.LastError = ""
	s.status = status
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Close(shutdownCtx); err != nil && s.opts.Logf != nil {
			s.opts.Logf("WARN parent tsnet shutdown failed: %v", err)
		}
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && s.opts.Logf != nil {
			s.opts.Logf("ERROR parent tsnet HTTP serve failed: %v", err)
		}
	}()
	return nil
}

func (s *ParentService) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	srv := s.httpServer
	node := s.node
	s.httpServer = nil
	s.listener = nil
	s.node = nil
	status := s.status
	status.Running = false
	s.status = status
	s.mu.Unlock()
	var errs []error
	if srv != nil {
		errs = append(errs, srv.Shutdown(ctx))
	}
	if node != nil {
		errs = append(errs, node.Close())
	}
	return firstError(errs...)
}

func (s *ParentService) Status() core.TailnetParentStatus {
	if s == nil {
		return core.TailnetParentStatus{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	status.Tags = append([]string(nil), status.Tags...)
	return status
}

func (s *ParentService) failStart(message string) error {
	err := fmt.Errorf("parent tsnet: %s", strings.TrimSpace(message))
	s.mu.Lock()
	status := s.status
	status.Enabled = true
	status.Running = false
	status.LastError = err.Error()
	s.status = status
	s.mu.Unlock()
	return err
}

type realParentNode struct {
	server *tsnet.Server
}

func newRealParentNode(opts ParentOptions) *realParentNode {
	server := &tsnet.Server{
		Dir:           strings.TrimSpace(opts.StateDir),
		Hostname:      strings.TrimSpace(opts.Hostname),
		AuthKey:       strings.TrimSpace(opts.AuthKey),
		AdvertiseTags: normalizeList(opts.Tags),
	}
	if opts.Logf != nil {
		server.Logf = opts.Logf
	}
	return &realParentNode{server: server}
}

func (n *realParentNode) Start() error {
	if n == nil || n.server == nil {
		return fmt.Errorf("tsnet server is nil")
	}
	return n.server.Start()
}

func (n *realParentNode) Listen(network string, addr string) (net.Listener, error) {
	if n == nil || n.server == nil {
		return nil, fmt.Errorf("tsnet server is nil")
	}
	return n.server.Listen(network, addr)
}

func (n *realParentNode) LocalClient() (PeerIdentifier, error) {
	if n == nil || n.server == nil {
		return nil, fmt.Errorf("tsnet server is nil")
	}
	client, err := n.server.LocalClient()
	if err != nil {
		return nil, err
	}
	return localPeerIdentifier{client: client}, nil
}

func (n *realParentNode) Close() error {
	if n == nil || n.server == nil {
		return nil
	}
	return n.server.Close()
}

type localPeerIdentifier struct {
	client *local.Client
}

func (i localPeerIdentifier) IdentifyPeer(ctx context.Context, remoteAddr string) (core.TailnetPeerIdentity, error) {
	if i.client == nil {
		return core.TailnetPeerIdentity{}, fmt.Errorf("tailscale local client is nil")
	}
	who, err := i.client.WhoIs(ctx, remoteAddr)
	if err != nil {
		return core.TailnetPeerIdentity{}, err
	}
	identity := core.TailnetPeerIdentity{RemoteAddr: remoteAddr}
	if who != nil {
		if who.Node != nil {
			identity.StableNodeID = string(who.Node.StableID)
			identity.NodeName = who.Node.Name
			identity.ComputedName = who.Node.ComputedName
			identity.Tags = append([]string(nil), who.Node.Tags...)
		}
		if who.UserProfile != nil {
			identity.LoginName = who.UserProfile.LoginName
		}
	}
	identity = core.NormalizeTailnetPeerIdentity(identity)
	if identity.StableNodeID == "" {
		return core.TailnetPeerIdentity{}, fmt.Errorf("tailscale peer identity has no stable node id")
	}
	return identity, nil
}

func attachPeerIdentity(handler http.Handler, identifier PeerIdentifier) http.Handler {
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL != nil && r.URL.Path == "/healthz" {
			handler.ServeHTTP(w, r)
			return
		}
		if identifier == nil {
			http.Error(w, "tailnet peer identity unavailable", http.StatusForbidden)
			return
		}
		identity, err := identifier.IdentifyPeer(r.Context(), r.RemoteAddr)
		if err != nil {
			http.Error(w, "tailnet peer identity unavailable", http.StatusForbidden)
			return
		}
		handler.ServeHTTP(w, r.WithContext(core.WithTailnetPeerIdentity(r.Context(), identity)))
	})
}

func parentStateInitialized(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	for _, rel := range []string{"tailscaled.state", "tailscaled.log.conf"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

func ParentMagicDNSURL(hostname string, tailnetName string, listenAddr string) string {
	hostname = strings.Trim(strings.TrimSpace(hostname), ".")
	tailnetName = strings.Trim(strings.TrimSpace(tailnetName), ".")
	if hostname == "" || tailnetName == "" {
		return ""
	}
	port := ""
	if strings.HasPrefix(listenAddr, ":") {
		port = strings.TrimPrefix(listenAddr, ":")
	} else if _, rawPort, err := net.SplitHostPort(listenAddr); err == nil {
		port = rawPort
	}
	if port == "" || port == "80" {
		return "http://" + hostname + "." + tailnetName
	}
	return "http://" + hostname + "." + tailnetName + ":" + port
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
	}
	return nil
}
