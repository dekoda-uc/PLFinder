// reconHound - concurrent IP range / port scanner with a native GUI
// (WebKit window via github.com/webview/webview_go).
// Build it and run it: it opens its own window, no browser, no
// internet required.
//
//	go build -o reconhound .
//	./reconhound
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	webview "github.com/webview/webview_go"
)

//go:embed web
var webFiles embed.FS

var (
	flagAddr  = flag.String("addr", "", "Fixed address for the interface to listen on (e.g. 127.0.0.1:8787). If omitted, a free port is chosen automatically")
	flagDebug = flag.Bool("debug", false, "Opens WebKit's developer tools inside the window")

	// Flags to preload the scan configuration at startup: the window
	// opens with these values already filled in the form, ready for
	// "Start search" (they do not start the scan by themselves).
	flagRanges    = flag.String("ranges", "", "Ranges to preload: path to a file (plain text, one per line, or JSON with {\"ranges\":[{\"start\":\"...\",\"end\":\"...\"}]}) or the value directly inline")
	flagPorts     = flag.String("ports", "", "Ports to preload, e.g.: 80,443,8000-8010 — or a path to a file with that content")
	flagConns     = flag.Int("connections", 0, "Connections to preload (0 = keeps the form's default value)")
	flagTimeout   = flag.Int("timeout", 0, "Timeout in seconds to preload (0 = keeps the default value)")
	flagScheme    = flag.String("scheme", "", "Scheme to preload: http, https or both")
	flagMatch     = flag.String("match", "", "Server match string to preload")
	flagResources = flag.String("resources", "", "Resources to preload, e.g.: /hello.txt,/hello.html")
	flagOkCodes   = flag.String("okcodes", "", "OK codes to preload, e.g.: 200,204")
	flagTCPOnly   = flag.Bool("tcponly", false, "Preloads the 'TCP only' option checked")
)

const maxBodyBytes = 1 << 20 // 1 MiB max stored per resource

// ================= Types =================

// InitialConfig is what the window reads at startup to preload the
// form (from the command-line flags). It does not trigger the scan
// by itself: it only fills in the fields.
type InitialConfig struct {
	HasConfig   bool   `json:"hasConfig"`
	Ranges      string `json:"ranges,omitempty"`
	Ports       string `json:"ports,omitempty"`
	Connections int    `json:"connections,omitempty"`
	TimeoutSec  int    `json:"timeoutSec,omitempty"`
	Scheme      string `json:"scheme,omitempty"`
	Match       string `json:"match,omitempty"`
	Resources   string `json:"resources,omitempty"`
	OkCodes     string `json:"okCodes,omitempty"`
	TCPOnly     bool   `json:"tcpOnly,omitempty"`
}

// rangesImportJSON is the schema of BGP/ASN-style range exports that
// should be importable with -ranges, e.g.:
// {"ranges":[{"cidr":"1.2.3.0/24","start":"1.2.3.0","end":"1.2.3.255"}]}
type rangesImportJSON struct {
	Ranges []struct {
		CIDR  string `json:"cidr"`
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"ranges"`
}

// loadRangesArg interprets the -ranges value: if it is the path to an
// existing file, it reads its content; otherwise it uses the value as-is
// (literal). In both cases, if the content turns out to be JSON with the
// schema above, it builds the range list (one per line) using "start - end"
// for each entry (so they stay editable by hand, e.g. to widen a range by
// joining several entries); if an entry has no start/end, its CIDR is used
// as a fallback. If the content is not recognizable JSON, it is used as-is
// (regular plain-text format).
func loadRangesArg(value string) string {
	content := value
	if info, err := os.Stat(value); err == nil && !info.IsDir() {
		if data, err := os.ReadFile(value); err == nil {
			content = string(data)
		}
	}
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "{") {
		var imp rangesImportJSON
		if err := json.Unmarshal([]byte(trimmed), &imp); err == nil && len(imp.Ranges) > 0 {
			var lines []string
			for _, r := range imp.Ranges {
				switch {
				case r.Start != "" && r.End != "":
					lines = append(lines, r.Start+" - "+r.End)
				case r.CIDR != "":
					lines = append(lines, r.CIDR)
				}
			}
			if len(lines) > 0 {
				return strings.Join(lines, "\n")
			}
		}
	}
	return content
}

// loadFileOrLiteral: if value is the path to an existing file, returns its
// content; otherwise returns value as-is (for -ports and similar flags).
func loadFileOrLiteral(value string) string {
	if info, err := os.Stat(value); err == nil && !info.IsDir() {
		if data, err := os.ReadFile(value); err == nil {
			return string(data)
		}
	}
	return value
}

func buildInitialConfig() InitialConfig {
	cfg := InitialConfig{}
	if *flagRanges != "" {
		cfg.Ranges = loadRangesArg(*flagRanges)
		cfg.HasConfig = true
	}
	if *flagPorts != "" {
		cfg.Ports = loadFileOrLiteral(*flagPorts)
		cfg.HasConfig = true
	}
	if *flagConns > 0 {
		cfg.Connections = *flagConns
		cfg.HasConfig = true
	}
	if *flagTimeout > 0 {
		cfg.TimeoutSec = *flagTimeout
		cfg.HasConfig = true
	}
	if *flagScheme != "" {
		cfg.Scheme = *flagScheme
		cfg.HasConfig = true
	}
	if *flagMatch != "" {
		cfg.Match = *flagMatch
		cfg.HasConfig = true
	}
	if *flagResources != "" {
		cfg.Resources = loadFileOrLiteral(*flagResources)
		cfg.HasConfig = true
	}
	if *flagOkCodes != "" {
		cfg.OkCodes = *flagOkCodes
		cfg.HasConfig = true
	}
	if *flagTCPOnly {
		cfg.TCPOnly = true
		cfg.HasConfig = true
	}
	return cfg
}

type ScanConfig struct {
	Ranges      string `json:"ranges"`
	Ports       string `json:"ports"`
	Connections int    `json:"connections"`
	TimeoutSec  int    `json:"timeoutSec"`
	Scheme      string `json:"scheme"` // http | https | both
	Match       string `json:"match"`
	Resources   string `json:"resources"`
	OkCodes     string `json:"okCodes"`
	TCPOnly     bool   `json:"tcpOnly"`
}

type ResourceInfo struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	Size        int    `json:"size"`
	Truncated   bool   `json:"truncated,omitempty"`
	HasBody     bool   `json:"hasBody"`
	OK          bool   `json:"ok"` // true if the status is among the configured "OK codes"
}

type ScanResult struct {
	ID          int                     `json:"id"`
	IP          string                  `json:"ip"`
	Port        int                     `json:"port"`
	Scheme      string                  `json:"scheme"`
	Server      string                  `json:"server"`
	ServerMatch bool                    `json:"serverMatch"`
	Resources   map[string]ResourceInfo `json:"resources"`
	ResourceHit bool                    `json:"resourceHit"`
}

type Stats struct {
	Scanned    int64   `json:"scanned"`
	Open       int64   `json:"open"`
	Matches    int64   `json:"matches"`
	Running    bool    `json:"running"`
	Paused     bool    `json:"paused"`
	ElapsedSec float64 `json:"elapsedSec"`
	Current    string  `json:"current"`
	Total      int64   `json:"total"` // 0 si no se pudo calcular
}

// ================= Server state =================

type Server struct {
	mu           sync.RWMutex
	results      []ScanResult
	resultsByID  map[int]ScanResult
	resourceBody map[int]map[string][]byte
	nextID       int
	totalTargets int64

	scanned int64
	open    int64
	matches int64
	running int32
	paused  int32

	cancel    context.CancelFunc
	startTime time.Time
	endTime   time.Time

	curMu  sync.Mutex
	curTgt string

	initialConfig InitialConfig

	hub *Hub
}

func NewServer() *Server {
	return &Server{
		hub:          NewHub(),
		resultsByID:  map[int]ScanResult{},
		resourceBody: map[int]map[string][]byte{},
	}
}

func (s *Server) resetForNewScan() {
	s.mu.Lock()
	s.results = nil
	s.resultsByID = map[int]ScanResult{}
	s.resourceBody = map[int]map[string][]byte{}
	s.nextID = 1
	s.totalTargets = 0
	s.startTime = time.Time{}
	s.endTime = time.Time{}
	s.mu.Unlock()
	atomic.StoreInt64(&s.scanned, 0)
	atomic.StoreInt64(&s.open, 0)
	atomic.StoreInt64(&s.matches, 0)
	s.setCurrent("")
}

func (s *Server) setCurrent(t string) {
	s.curMu.Lock()
	s.curTgt = t
	s.curMu.Unlock()
}

func (s *Server) getCurrent() string {
	s.curMu.Lock()
	defer s.curMu.Unlock()
	return s.curTgt
}

func (s *Server) stats() Stats {
	s.mu.RLock()
	start := s.startTime
	end := s.endTime
	total := s.totalTargets
	s.mu.RUnlock()

	running := atomic.LoadInt32(&s.running) == 1
	elapsed := 0.0
	if !start.IsZero() {
		if running {
			elapsed = time.Since(start).Seconds()
		} else if !end.IsZero() {
			elapsed = end.Sub(start).Seconds() // se congela al terminar
		}
	}

	return Stats{
		Scanned:    atomic.LoadInt64(&s.scanned),
		Open:       atomic.LoadInt64(&s.open),
		Matches:    atomic.LoadInt64(&s.matches),
		Running:    running,
		Paused:     atomic.LoadInt32(&s.paused) == 1,
		ElapsedSec: elapsed,
		Current:    s.getCurrent(),
		Total:      total,
	}
}

// ================= Server-Sent Events hub =================

type Hub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func NewHub() *Hub { return &Hub{clients: map[chan []byte]struct{}{}} }

func (h *Hub) Register() chan []byte {
	ch := make(chan []byte, 512)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unregister(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *Hub) Broadcast(event string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := append([]byte("event: "+event+"\ndata: "), payload...)
	msg = append(msg, []byte("\n\n")...)

	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

// ================= main =================

func main() {
	flag.Parse()
	srv := NewServer()
	srv.initialConfig = buildInitialConfig()

	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/start", srv.handleStart)
	mux.HandleFunc("/api/pause", srv.handlePause)
	mux.HandleFunc("/api/resume", srv.handleResume)
	mux.HandleFunc("/api/stop", srv.handleStop)
	mux.HandleFunc("/api/stats", srv.handleStats)
	mux.HandleFunc("/api/initial-config", srv.handleInitialConfig)
	mux.HandleFunc("/api/events", srv.handleEvents)
	mux.HandleFunc("/api/export", srv.handleExport)
	mux.HandleFunc("/api/export-resources", srv.handleExportResources)
	mux.HandleFunc("/api/resource-content", srv.handleResourceContent)

	addr := *flagAddr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal("could not open the port: ", err)
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())

	go func() {
		if err := http.Serve(ln, mux); err != nil {
			log.Println("server stopped:", err)
		}
	}()

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			srv.hub.Broadcast("stats", srv.stats())
		}
	}()

	log.Println("Interface available at", url)

	w := webview.New(*flagDebug)
	defer w.Destroy()
	w.SetTitle("reconHound")
	w.SetSize(1180, 820, webview.HintNone)
	w.Navigate(url)
	w.Run()
}

// ================= HTTP handlers =================

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if atomic.LoadInt32(&s.running) == 1 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a scan is already running"})
		return
	}

	var cfg ScanConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(cfg.Ranges) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing IP ranges"})
		return
	}
	if cfg.Connections <= 0 {
		cfg.Connections = 400
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 4
	}
	if cfg.Scheme == "" {
		cfg.Scheme = "http"
	}

	s.resetForNewScan()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.startTime = time.Now()
	s.mu.Unlock()
	atomic.StoreInt32(&s.running, 1)
	atomic.StoreInt32(&s.paused, 0)

	go s.runScan(ctx, cfg)

	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	if atomic.LoadInt32(&s.running) == 1 {
		atomic.StoreInt32(&s.paused, 1)
	}
	s.hub.Broadcast("stats", s.stats())
	writeJSON(w, http.StatusOK, s.stats())
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	atomic.StoreInt32(&s.paused, 0)
	s.hub.Broadcast("stats", s.stats())
	writeJSON(w, http.StatusOK, s.stats())
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	atomic.StoreInt32(&s.paused, 0)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.stats())
}

// handleInitialConfig returns the configuration preloaded from the
// command-line flags (-ranges, -ports, etc.), so the window fills in the
// form at startup without triggering the scan by itself.
func (s *Server) handleInitialConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.initialConfig)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming no soportado", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.hub.Register()
	defer s.hub.Unregister(ch)

	s.mu.RLock()
	snapshot := make([]ScanResult, len(s.results))
	copy(snapshot, s.results)
	s.mu.RUnlock()
	for _, res := range snapshot {
		payload, _ := json.Marshal(res)
		fmt.Fprintf(w, "event: result\ndata: %s\n\n", payload)
	}
	statsPayload, _ := json.Marshal(s.stats())
	fmt.Fprintf(w, "event: stats\ndata: %s\n\n", statsPayload)
	flusher.Flush()

	for {
		select {
		case msg, okCh := <-ch:
			if !okCh {
				return
			}
			w.Write(msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

type exportRequest struct {
	Scope string `json:"scope"` // "all" | "selected"
	IDs   []int  `json:"ids"`
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if r.Method == http.MethodPost {
		json.NewDecoder(r.Body).Decode(&req)
	}
	wantAll := req.Scope != "selected"
	idSet := map[int]bool{}
	for _, id := range req.IDs {
		idSet[id] = true
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	resourceCols := []string{}
	seenCol := map[string]bool{}
	for _, res := range s.results {
		for k := range res.Resources {
			if !seenCol[k] {
				seenCol[k] = true
				resourceCols = append(resourceCols, k)
			}
		}
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=\"results.csv\"")
	cw := csv.NewWriter(w)
	header := []string{"IP", "Port", "Scheme", "Server", "ServerMatch"}
	header = append(header, resourceCols...)
	cw.Write(header)

	for _, res := range s.results {
		if !wantAll && !idSet[res.ID] {
			continue
		}
		row := []string{res.IP, strconv.Itoa(res.Port), res.Scheme, res.Server, strconv.FormatBool(res.ServerMatch)}
		for _, col := range resourceCols {
			if info, ok := res.Resources[col]; ok {
				row = append(row, strconv.Itoa(info.Status))
			} else {
				row = append(row, "")
			}
		}
		cw.Write(row)
	}
	cw.Flush()
}

// handleResourceContent serves the cached content of a specific resource,
// used by the interface's "View resource" button.
func (s *Server) handleResourceContent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	path := r.URL.Query().Get("path")

	s.mu.RLock()
	res, okRes := s.resultsByID[id]
	bodies, okBody := s.resourceBody[id]
	s.mu.RUnlock()

	if !okRes || !okBody {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	data, okData := bodies[path]
	info, okInfo := res.Resources[path]
	if !okData || !okInfo {
		http.Error(w, "resource not found", http.StatusNotFound)
		return
	}

	ct := info.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Status-Code", strconv.Itoa(info.Status))
	if info.Truncated {
		w.Header().Set("X-Truncated", "true")
	}
	w.Write(data)
}

type exportResourcesRequest struct {
	Scope string `json:"scope"`
	IDs   []int  `json:"ids"`
}

// handleExportResources builds a ZIP with the actual content of the
// resources captured during the scan, for the selected row or rows.
func (s *Server) handleExportResources(w http.ResponseWriter, r *http.Request) {
	var req exportResourcesRequest
	if r.Method == http.MethodPost {
		json.NewDecoder(r.Body).Decode(&req)
	}
	wantAll := req.Scope != "selected"
	idSet := map[int]bool{}
	for _, id := range req.IDs {
		idSet[id] = true
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	used := map[string]bool{}
	count := 0

	for id, bodies := range s.resourceBody {
		if !wantAll && !idSet[id] {
			continue
		}
		res, ok := s.resultsByID[id]
		if !ok {
			continue
		}
		for path, data := range bodies {
			info, hasInfo := res.Resources[path]
			if !hasInfo || !info.OK {
				continue // only resources that actually exist (OK status) get saved
			}
			name := uniqueFilename(sanitizeFilename(fmt.Sprintf("%s_%d%s", res.IP, res.Port, path)), used)
			fw, err := zw.Create(name)
			if err != nil {
				continue
			}
			fw.Write(data)
			count++
		}
	}
	zw.Close()

	if count == 0 {
		http.Error(w, "no OK-status resources to save for this selection", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="recursos.zip"`)
	w.Write(buf.Bytes())
}

func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "?", "_", "*", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", " ", "_",
	)
	out := replacer.Replace(s)
	out = strings.Trim(out, "_")
	if out == "" {
		out = "resource"
	}
	return out
}

func uniqueFilename(base string, used map[string]bool) string {
	name := base
	n := 1
	for used[name] {
		n++
		name = fmt.Sprintf("%s_%d", base, n)
	}
	used[name] = true
	return name
}

// ================= Scan engine =================

type Job struct {
	IP   string
	Port int
}

type scanEngineConfig struct {
	schemes   []string
	timeout   time.Duration
	match     string
	resources []string
	okCodes   map[int]bool
	tcpOnly   bool
}

func (s *Server) runScan(ctx context.Context, cfg ScanConfig) {
	defer func() {
		atomic.StoreInt32(&s.running, 0)
		atomic.StoreInt32(&s.paused, 0)
		s.mu.Lock()
		s.endTime = time.Now()
		s.mu.Unlock()
		s.setCurrent("")
		s.hub.Broadcast("stats", s.stats())
		s.hub.Broadcast("done", map[string]string{"status": "finished"})
	}()

	rangeLines, err := parseList(cfg.Ranges)
	if err != nil || len(rangeLines) == 0 {
		s.hub.Broadcast("error", map[string]string{"error": "invalid IP ranges"})
		return
	}
	ports, err := parsePorts(cfg.Ports)
	if err != nil || len(ports) == 0 {
		s.hub.Broadcast("error", map[string]string{"error": "invalid ports"})
		return
	}

	var totalIPs uint64
	for _, line := range rangeLines {
		if n, err := countIPsInLine(line); err == nil {
			totalIPs += n
		}
	}
	s.mu.Lock()
	s.totalTargets = int64(totalIPs * uint64(len(ports)))
	s.mu.Unlock()

	var resources []string
	for _, r := range strings.Split(cfg.Resources, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !strings.HasPrefix(r, "/") {
			r = "/" + r
		}
		resources = append(resources, r)
	}

	okCodes := map[int]bool{}
	for _, c := range strings.Split(cfg.OkCodes, ",") {
		c = strings.TrimSpace(c)
		if n, err := strconv.Atoi(c); err == nil {
			okCodes[n] = true
		}
	}
	if len(okCodes) == 0 {
		okCodes[200] = true
	}

	var schemes []string
	switch cfg.Scheme {
	case "both":
		schemes = []string{"http", "https"}
	case "https":
		schemes = []string{"https"}
	default:
		schemes = []string{"http"}
	}

	engCfg := scanEngineConfig{
		schemes:   schemes,
		timeout:   time.Duration(cfg.TimeoutSec) * time.Second,
		match:     strings.ToLower(cfg.Match),
		resources: resources,
		okCodes:   okCodes,
		tcpOnly:   cfg.TCPOnly,
	}

	ipChan := make(chan string, 2048)
	jobChan := make(chan Job, 2048)

	var ipWG sync.WaitGroup
	ipWG.Add(1)
	go func() {
		defer ipWG.Done()
		defer close(ipChan)
		for _, line := range rangeLines {
			_ = emitIPsFromLine(ctx, line, ipChan)
		}
	}()

	var jobWG sync.WaitGroup
	jobWG.Add(1)
	go func() {
		defer jobWG.Done()
		defer close(jobChan)
		for ip := range ipChan {
			for _, p := range ports {
				select {
				case jobChan <- Job{IP: ip, Port: p}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	numWorkers := cfg.Connections
	var workersWG sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			for job := range jobChan {
				for atomic.LoadInt32(&s.paused) == 1 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(150 * time.Millisecond):
					}
				}
				select {
				case <-ctx.Done():
					return
				default:
				}

				s.setCurrent(fmt.Sprintf("%s:%d", job.IP, job.Port))

				res, bodies, open := scanJob(ctx, job, engCfg)
				atomic.AddInt64(&s.scanned, 1)
				if !open {
					continue
				}
				atomic.AddInt64(&s.open, 1)

				anyHit := false
				for _, info := range res.Resources {
					if engCfg.okCodes[info.Status] {
						anyHit = true
						break
					}
				}
				res.ResourceHit = anyHit
				if res.ServerMatch || anyHit {
					atomic.AddInt64(&s.matches, 1)
				}

				s.mu.Lock()
				res.ID = s.nextID
				s.nextID++
				s.results = append(s.results, res)
				s.resultsByID[res.ID] = res
				if len(bodies) > 0 {
					s.resourceBody[res.ID] = bodies
				}
				s.mu.Unlock()

				s.hub.Broadcast("result", res)
			}
		}()
	}

	workersWG.Wait()
	ipWG.Wait()
	jobWG.Wait()
}

// scanJob tries to open the TCP connection and, if applicable, makes
// HTTP requests to get the Server header and download (size-limited)
// the content of the requested resources.
func scanJob(ctx context.Context, job Job, cfg scanEngineConfig) (ScanResult, map[string][]byte, bool) {
	result := ScanResult{IP: job.IP, Port: job.Port, Resources: map[string]ResourceInfo{}}
	bodies := map[string][]byte{}

	addr := net.JoinHostPort(job.IP, strconv.Itoa(job.Port))
	dialer := net.Dialer{Timeout: cfg.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return result, bodies, false
	}
	conn.Close()

	if cfg.tcpOnly {
		return result, bodies, true
	}

	client := &http.Client{
		Timeout: cfg.timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, scheme := range cfg.schemes {
		base := fmt.Sprintf("%s://%s", scheme, addr)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/", nil)
		if err == nil {
			req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; reconHound/1.0)")
			if resp, err := client.Do(req); err == nil {
				if result.Server == "" {
					result.Scheme = scheme
					result.Server = resp.Header.Get("Server")
					if cfg.match != "" && strings.Contains(strings.ToLower(result.Server), cfg.match) {
						result.ServerMatch = true
					}
				}
				io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
				resp.Body.Close()
			}
		}

		for _, resPath := range cfg.resources {
			if existing, ok := result.Resources[resPath]; ok && cfg.okCodes[existing.Status] {
				continue // already found on a previous scheme
			}
			rreq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+resPath, nil)
			if err != nil {
				continue
			}
			rreq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; reconHound/1.0)")
			rresp, err := client.Do(rreq)
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(io.LimitReader(rresp.Body, maxBodyBytes+1))
			rresp.Body.Close()

			truncated := false
			if len(data) > maxBodyBytes {
				data = data[:maxBodyBytes]
				truncated = true
			}
			ct := rresp.Header.Get("Content-Type")
			if ct == "" {
				ct = http.DetectContentType(data)
			}
			result.Resources[resPath] = ResourceInfo{
				Status:      rresp.StatusCode,
				ContentType: ct,
				Size:        len(data),
				Truncated:   truncated,
				HasBody:     true,
				OK:          cfg.okCodes[rresp.StatusCode],
			}
			bodies[resPath] = data
		}
	}

	return result, bodies, true
}

// ================= Range and port parsing =================

func parseList(s string) ([]string, error) {
	if info, err := os.Stat(s); err == nil && !info.IsDir() {
		data, err := os.ReadFile(s)
		if err != nil {
			return nil, err
		}
		s = string(data)
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for _, part := range strings.Split(line, ",") {
			part = strings.TrimSpace(part)
			if part == "" || strings.HasPrefix(part, "#") {
				continue
			}
			out = append(out, part)
		}
	}
	return out, nil
}

func parsePorts(s string) ([]int, error) {
	raw, err := parseList(s)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var ports []int
	for _, p := range raw {
		if strings.Contains(p, "-") {
			parts := strings.SplitN(p, "-", 2)
			start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 != nil || err2 != nil || start > end {
				continue
			}
			for port := start; port <= end; port++ {
				if !seen[port] {
					seen[port] = true
					ports = append(ports, port)
				}
			}
		} else {
			port, err := strconv.Atoi(p)
			if err != nil {
				continue
			}
			if !seen[port] {
				seen[port] = true
				ports = append(ports, port)
			}
		}
	}
	return ports, nil
}

func emitIPsFromLine(ctx context.Context, line string, out chan<- string) error {
	line = strings.TrimSpace(line)

	if strings.Contains(line, "/") {
		ip, ipnet, err := net.ParseCIDR(line)
		if err != nil {
			return err
		}
		start := ipToUint32(ip.Mask(ipnet.Mask))
		mask := ipToUint32(net.IP(ipnet.Mask))
		end := start | ^mask
		return emitRange(ctx, start, end, out)
	}

	if strings.Contains(line, "-") {
		parts := strings.SplitN(line, "-", 2)
		startStr := strings.TrimSpace(parts[0])
		endStr := strings.TrimSpace(parts[1])

		startIP := net.ParseIP(startStr).To4()
		if startIP == nil {
			return fmt.Errorf("invalid start IP: %q", startStr)
		}

		var endIP net.IP
		if strings.Contains(endStr, ".") {
			endIP = net.ParseIP(endStr).To4()
			if endIP == nil {
				return fmt.Errorf("invalid end IP: %q", endStr)
			}
		} else {
			lastOctet, err := strconv.Atoi(endStr)
			if err != nil || lastOctet < 0 || lastOctet > 255 {
				return fmt.Errorf("invalid last octet: %q", endStr)
			}
			endIP = make(net.IP, 4)
			copy(endIP, startIP)
			endIP[3] = byte(lastOctet)
		}

		start := ipToUint32(startIP)
		end := ipToUint32(endIP)
		if end < start {
			return fmt.Errorf("reversed range: %s-%s", startStr, endStr)
		}
		return emitRange(ctx, start, end, out)
	}

	ip := net.ParseIP(line).To4()
	if ip == nil {
		return fmt.Errorf("invalid IP: %q", line)
	}
	select {
	case out <- ip.String():
	case <-ctx.Done():
	}
	return nil
}

func emitRange(ctx context.Context, start, end uint32, out chan<- string) error {
	for v := start; ; v++ {
		select {
		case out <- uint32ToIP(v).String():
		case <-ctx.Done():
			return nil
		}
		if v == end {
			break
		}
	}
	return nil
}

// countIPsInLine counts how many IPs a range line represents, without
// expanding them, so the scan's total progress can be shown.
func countIPsInLine(line string) (uint64, error) {
	line = strings.TrimSpace(line)

	if strings.Contains(line, "/") {
		_, ipnet, err := net.ParseCIDR(line)
		if err != nil {
			return 0, err
		}
		ones, bits := ipnet.Mask.Size()
		if bits != 32 {
			return 0, fmt.Errorf("only IPv4 is supported")
		}
		return uint64(1) << uint(32-ones), nil
	}

	if strings.Contains(line, "-") {
		parts := strings.SplitN(line, "-", 2)
		startStr := strings.TrimSpace(parts[0])
		endStr := strings.TrimSpace(parts[1])
		startIP := net.ParseIP(startStr).To4()
		if startIP == nil {
			return 0, fmt.Errorf("invalid IP")
		}
		var endIP net.IP
		if strings.Contains(endStr, ".") {
			endIP = net.ParseIP(endStr).To4()
			if endIP == nil {
				return 0, fmt.Errorf("invalid IP")
			}
		} else {
			lastOctet, err := strconv.Atoi(endStr)
			if err != nil || lastOctet < 0 || lastOctet > 255 {
				return 0, fmt.Errorf("invalid octet")
			}
			endIP = make(net.IP, 4)
			copy(endIP, startIP)
			endIP[3] = byte(lastOctet)
		}
		start := ipToUint32(startIP)
		end := ipToUint32(endIP)
		if end < start {
			return 0, fmt.Errorf("reversed range")
		}
		return uint64(end-start) + 1, nil
	}

	if net.ParseIP(line).To4() == nil {
		return 0, fmt.Errorf("invalid IP")
	}
	return 1, nil
}

func ipToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	return binary.BigEndian.Uint32(ip4)
}

func uint32ToIP(v uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, v)
	return ip
}
