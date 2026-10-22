package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/webview/webview_go"
)

//go:embed index.html
var content embed.FS

type ScanRequest struct {
	Target string `json:"target"`
}

type ScanResponse struct {
	Success    bool            `json:"success"`
	Message    string          `json:"message"`
	IP         string          `json:"ip"`
	Port       string          `json:"port"`
	LookupData json.RawMessage `json:"lookup_data,omitempty"` // Contenido raw de last_lookup.json
	Error      string          `json:"error,omitempty"`
}

func main() {
	if err := os.MkdirAll("output", 0755); err != nil {
		log.Fatalf("Error creating output folder: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Error starting local listener: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/scan", handleScan)

	go func() {
		if err := http.Serve(listener, mux); err != nil {
			log.Fatal(err)
		}
	}()

	debug := false
	w := webview.New(debug)
	defer w.Destroy()
	w.SetTitle("PLfinder Astra")
	w.SetSize(640, 320, webview.HintFixed)
	w.Navigate(serverURL)
	w.Run()
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(content, "index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

func handleScan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(ScanResponse{Success: false, Error: "Method not allowed"})
		return
	}

	var req ScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ScanResponse{Success: false, Error: "Invalid JSON"})
		return
	}

	ip, port, err := parseInput(req.Target)
	if err != nil {
		json.NewEncoder(w).Encode(ScanResponse{Success: false, Error: err.Error()})
		return
	}

	// 1. Ejecutar asnlookup.exe --ip-version 4 <IP>
	fmt.Printf("-> Running asnlookup.exe for IP: %s\n", ip)
	cmdIPS := exec.Command("./asnlookup.exe", "--ip-version", "4", ip)
	if runtime.GOOS == "windows" {
		cmdIPS.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	}

	if err := cmdIPS.Run(); err != nil {
		json.NewEncoder(w).Encode(ScanResponse{
			Success: false,
			Error:   fmt.Sprintf("Error running asnlookup.exe: %v", err),
		})
		return
	}

	lookupFile := filepath.Join("output", "last_lookup.json")
	lookupBytes, err := os.ReadFile(lookupFile)
	if err != nil {
		json.NewEncoder(w).Encode(ScanResponse{
			Success: false,
			Error:   "asnlookup.exe finished but output/last_lookup.json could not be read",
		})
		return
	}

	// 2. Lanzar reconhound.exe de forma asíncrona en su propia ventana
	fmt.Printf("-> Launching reconhound.exe in independent process with Port: %s\n", port)
	cmdL1 := exec.Command("./reconhound.exe",
		"-ranges", lookupFile,
		"-ports", port,
		"-timeout", "3",
		"-match", "Astra",
		"-resources", "/playlist.m3u8",
	)

	if runtime.GOOS == "windows" {
		cmdL1.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x00000010, // CREATE_NEW_CONSOLE
		}
	}

	if err := cmdL1.Start(); err != nil {
		json.NewEncoder(w).Encode(ScanResponse{
			Success: false,
			Error:   fmt.Sprintf("Error launching reconhound.exe: %v", err),
		})
		return
	}

	// Responder con el contenido del JSON en lookup_data
	json.NewEncoder(w).Encode(ScanResponse{
		Success:    true,
		Message:    "Process completed successfully.",
		IP:         ip,
		Port:       port,
		LookupData: json.RawMessage(lookupBytes),
	})
}

func parseInput(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("Target field is empty")
	}

	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("Invalid target format")
	}

	host := u.Hostname()
	port := u.Port()

	if host == "" {
		return "", "", fmt.Errorf("Could not extract IP")
	}

	if port == "" {
		port = "8000"
	}

	if net.ParseIP(host) == nil {
		return "", "", fmt.Errorf("IP '%s' is not a valid IPv4 address", host)
	}

	return host, port, nil
}