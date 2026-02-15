package sites

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"nhooyr.io/websocket"
)

// LiveReloadServer watches site file changes and notifies browsers via WebSocket.
type LiveReloadServer struct {
	mu       sync.Mutex
	watchers map[string]context.CancelFunc // siteID → cancel
	clients  map[*websocket.Conn]bool
	clientMu sync.Mutex
	server   *http.Server
	port     int
}

// NewLiveReloadServer creates a new live reload server.
func NewLiveReloadServer() *LiveReloadServer {
	return &LiveReloadServer{
		watchers: make(map[string]context.CancelFunc),
		clients:  make(map[*websocket.Conn]bool),
		port:     35729,
	}
}

// Start begins the HTTP/WebSocket server in the background.
func (lr *LiveReloadServer) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", lr.handleWebSocket)
	mux.HandleFunc("/livereload.js", lr.serveScript)

	lr.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", lr.port),
		Handler: mux,
	}

	go func() {
		if err := lr.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("LiveReload server error: " + err.Error())
		}
	}()
}

// Stop shuts down the server and all watchers.
func (lr *LiveReloadServer) Stop() {
	lr.mu.Lock()
	for id, cancel := range lr.watchers {
		cancel()
		delete(lr.watchers, id)
	}
	lr.mu.Unlock()

	if lr.server != nil {
		lr.server.Shutdown(context.Background())
	}
}

// EnableForSite starts a file watcher for the given site.
func (lr *LiveReloadServer) EnableForSite(siteID, filesDir string) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	if _, exists := lr.watchers[siteID]; exists {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	lr.watchers[siteID] = cancel

	go lr.watchFiles(ctx, filesDir)
}

// DisableForSite stops the file watcher for the given site.
func (lr *LiveReloadServer) DisableForSite(siteID string) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	if cancel, ok := lr.watchers[siteID]; ok {
		cancel()
		delete(lr.watchers, siteID)
	}
}

func (lr *LiveReloadServer) watchFiles(ctx context.Context, dir string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("Failed to create file watcher: " + err.Error())
		return
	}
	defer watcher.Close()

	// Watch all subdirectories.
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			watcher.Add(p)
		}
		return nil
	})

	// Debounce: wait 200ms after last change before signaling reload.
	var timer *time.Timer
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) {
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(200*time.Millisecond, func() {
					lr.broadcastReload()
				})
			}
			// Watch newly created directories.
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					watcher.Add(event.Name)
				}
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (lr *LiveReloadServer) broadcastReload() {
	lr.clientMu.Lock()
	defer lr.clientMu.Unlock()
	for conn := range lr.clients {
		conn.Write(context.Background(), websocket.MessageText, []byte("reload"))
	}
}

func (lr *LiveReloadServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}

	lr.clientMu.Lock()
	lr.clients[conn] = true
	lr.clientMu.Unlock()

	// Block until the connection is closed.
	for {
		_, _, err := conn.Read(context.Background())
		if err != nil {
			lr.clientMu.Lock()
			delete(lr.clients, conn)
			lr.clientMu.Unlock()
			return
		}
	}
}

func (lr *LiveReloadServer) serveScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	fmt.Fprintf(w, `(function(){var ws=new WebSocket("ws://"+location.hostname+":%d/ws");ws.onmessage=function(){location.reload();};ws.onclose=function(){setTimeout(function(){location.reload();},2000);}})();`, lr.port)
}
