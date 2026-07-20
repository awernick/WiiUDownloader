package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	wiiudownloader "github.com/Xpl0itU/WiiUDownloader"
)

const (
	NETWORK_DIAL_TIMEOUT         = 30 * time.Second
	NETWORK_DIAL_KEEP_ALIVE      = 30 * time.Second
	FALLBACK_DNS_DIAL_TIMEOUT    = 10 * time.Second
	FALLBACK_DNS_RESOLVER_ENDPOINT = "1.1.1.1:53"
	HTTP_MAX_IDLE_CONNS          = 100
	HTTP_MAX_IDLE_CONNS_PER_HOST = 100
	HTTP_MAX_CONNS_PER_HOST      = 100
	HTTP_IDLE_CONN_TIMEOUT       = 90 * time.Second
	HTTP_TLS_HANDSHAKE_TIMEOUT   = 10 * time.Second
	HTTP_RESPONSE_HEADER_TIMEOUT = 10 * time.Second
	HTTP_EXPECT_CONTINUE_TIMEOUT = 1 * time.Second
)

func buildHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: NETWORK_DIAL_TIMEOUT, KeepAlive: NETWORK_DIAL_KEEP_ALIVE}
				conn, err := dialer.DialContext(ctx, network, addr)
				if err == nil {
					return conn, nil
				}
				if !strings.Contains(err.Error(), "no such host") && !strings.Contains(err.Error(), "lookup") {
					return nil, err
				}
				log.Printf("DNS lookup failed for %s, retrying with 1.1.1.1...", addr)
				resolver := &net.Resolver{
					PreferGo: true,
					Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
						d := &net.Dialer{Timeout: FALLBACK_DNS_DIAL_TIMEOUT}
						return d.DialContext(ctx, "udp", FALLBACK_DNS_RESOLVER_ENDPOINT)
					},
				}
				host, port, splitErr := net.SplitHostPort(addr)
				if splitErr != nil {
					return nil, err
				}
				ips, lookupErr := resolver.LookupIPAddr(ctx, host)
				if lookupErr != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, err
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
			},
			MaxIdleConns:          HTTP_MAX_IDLE_CONNS,
			MaxIdleConnsPerHost:   HTTP_MAX_IDLE_CONNS_PER_HOST,
			MaxConnsPerHost:       HTTP_MAX_CONNS_PER_HOST,
			IdleConnTimeout:       HTTP_IDLE_CONN_TIMEOUT,
			TLSHandshakeTimeout:   HTTP_TLS_HANDSHAKE_TIMEOUT,
			ResponseHeaderTimeout: HTTP_RESPONSE_HEADER_TIMEOUT,
			ExpectContinueTimeout: HTTP_EXPECT_CONTINUE_TIMEOUT,
		},
	}
}

// cliProgress implements wiiudownloader.ProgressReporter for terminal output.
type cliProgress struct {
	mu                sync.Mutex
	gameTitle         string
	downloadSize      int64
	totalDownloaded   int64
	startTime         time.Time
	doneFiles         map[string]bool
	cancelled         atomic.Bool
	lastProgressPrint time.Time
}

func newCLIProgress() *cliProgress {
	return &cliProgress{
		doneFiles: make(map[string]bool),
	}
}

func (p *cliProgress) SetGameTitle(title string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gameTitle = title
	fmt.Printf("\n=== %s ===\n", title)
}

func (p *cliProgress) UpdateDownloadProgress(downloaded int64, filename string) {
	// With concurrent downloads, per-file progress on \r is unreadable.
	// File completion is reported via MarkFileAsDone, which is sufficient.
}

func (p *cliProgress) UpdateDecryptionProgress(progress float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if now.Sub(p.lastProgressPrint) < 500*time.Millisecond && progress < 1.0 {
		return
	}
	p.lastProgressPrint = now
	fmt.Printf("\r  Decrypting: %.1f%%", progress*100)
	if progress >= 1.0 {
		fmt.Println()
	}
}

func (p *cliProgress) Cancelled() bool {
	return p.cancelled.Load()
}

func (p *cliProgress) SetCancelled() {
	p.cancelled.Store(true)
}

func (p *cliProgress) SetDownloadSize(size int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.downloadSize = size
	p.totalDownloaded = 0
	fmt.Printf("  Total size: %s\n", humanBytes(size))
}

func (p *cliProgress) ResetTotals() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.totalDownloaded = 0
	p.downloadSize = 0
	p.doneFiles = make(map[string]bool)
}

func (p *cliProgress) MarkFileAsDone(filename string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.doneFiles[filename] {
		return
	}
	p.doneFiles[filename] = true
	fmt.Printf("\r  %-12s done                                \n", filename)
}

func (p *cliProgress) SetTotalDownloadedForFile(filename string, downloaded int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Aggregate per-file progress into total; the library calls this frequently.
}

func (p *cliProgress) SetStartTime(startTime time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.startTime = startTime
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func usage() {
	fmt.Fprintf(os.Stderr, `WiiUDownloader CLI - download and decrypt Wii U titles from Nintendo's servers.

Usage:
  wiiudownloader-cli download <titleID> <outputDir> [flags]
  wiiudownloader-cli decrypt <path> [flags]

Commands:
  download    Download a title by its 16-hex-char title ID.
  decrypt     Decrypt an already-downloaded title directory.

Download flags:
  -version int          Specific title version to download (0 = latest).
  -decrypt              Decrypt contents after download.
  -delete-encrypted     Delete encrypted files after successful decryption (requires -decrypt).
  -decrypt-output-dir   Directory for decrypted output (default: same as output dir).

Decrypt flags:
  -delete-encrypted     Delete encrypted files after successful decryption.
  -output-dir           Directory for decrypted output (default: same as input path).

Examples:
  wiiudownloader-cli download 0005000010184d00 ./YoshisWoollyWorld
  wiiudownloader-cli download 0005000010184d00 ./YoshisWoollyWorld -decrypt -delete-encrypted
  wiiudownloader-cli decrypt ./YoshisWoollyWorld -output-dir ./decrypted
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "download":
		cmdDownload(reorderArgs(os.Args[2:], downloadValueFlags))
	case "decrypt":
		cmdDecrypt(reorderArgs(os.Args[2:], decryptValueFlags))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

// reorderArgs moves all flag arguments before positional arguments so that
// Go's flag package, which stops at the first non-flag arg, can parse them
// regardless of where the user placed them on the command line.
// valueFlags is a set of flag names that take a value (non-boolean).
func reorderArgs(args []string, valueFlags map[string]bool) []string {
	var flags, positional []string
	i := 0
	for i < len(args) {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if !strings.Contains(name, "=") && valueFlags[name] {
				if i+1 < len(args) {
					flags = append(flags, args[i+1])
					i++
				}
			}
		} else {
			positional = append(positional, arg)
		}
		i++
	}
	return append(flags, positional...)
}

var downloadValueFlags = map[string]bool{
	"version":           true,
	"decrypt-output-dir": true,
}

var decryptValueFlags = map[string]bool{
	"output-dir": true,
}

func cmdDownload(args []string) {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	version := fs.Int("version", 0, "title version (0 = latest)")
	doDecrypt := fs.Bool("decrypt", false, "decrypt contents after download")
	deleteEnc := fs.Bool("delete-encrypted", false, "delete encrypted files after decryption")
	decryptOut := fs.String("decrypt-output-dir", "", "directory for decrypted output")
	fs.Usage = usage
	fs.Parse(args)

	rest := fs.Args()
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "download requires <titleID> <outputDir>")
		os.Exit(1)
	}

	titleID := rest[0]
	outputDir := rest[1]

	if len(titleID) != 16 {
		fmt.Fprintf(os.Stderr, "title ID must be 16 hex chars, got %d: %q\n", len(titleID), titleID)
		os.Exit(1)
	}
	if _, err := parseHexTitleID(titleID); err != nil {
		fmt.Fprintf(os.Stderr, "invalid title ID: %v\n", err)
		os.Exit(1)
	}

	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid output dir: %v\n", err)
		os.Exit(1)
	}

	absDecryptOut := ""
	if *decryptOut != "" {
		absDecryptOut, err = filepath.Abs(*decryptOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid decrypt output dir: %v\n", err)
			os.Exit(1)
		}
	}

	if *deleteEnc && !*doDecrypt {
		fmt.Fprintln(os.Stderr, "-delete-encrypted requires -decrypt")
		os.Exit(1)
	}

	progress := newCLIProgress()
	client := buildHTTPClient()

	fmt.Printf("Downloading title %s to %s\n", titleID, absOutput)
	if *version > 0 {
		fmt.Printf("  Version: %d\n", *version)
	}
	if *doDecrypt {
		fmt.Println("  Decrypt: yes")
		if *deleteEnc {
			fmt.Println("  Delete encrypted after decrypt: yes")
		}
		if absDecryptOut != "" {
			fmt.Printf("  Decrypt output: %s\n", absDecryptOut)
		}
	}
	fmt.Println()

	err = wiiudownloader.DownloadTitle(
		titleID,
		absOutput,
		*version,
		*doDecrypt,
		progress,
		*deleteEnc,
		client,
		absDecryptOut,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nDownload failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Download complete.")
}

func cmdDecrypt(args []string) {
	fs := flag.NewFlagSet("decrypt", flag.ExitOnError)
	deleteEnc := fs.Bool("delete-encrypted", false, "delete encrypted files after decryption")
	outputDir := fs.String("output-dir", "", "directory for decrypted output")
	fs.Usage = usage
	fs.Parse(args)

	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "decrypt requires <path>")
		os.Exit(1)
	}

	inputPath := rest[0]
	absInput, err := filepath.Abs(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid path: %v\n", err)
		os.Exit(1)
	}

	absOut := ""
	if *outputDir != "" {
		absOut, err = filepath.Abs(*outputDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid output dir: %v\n", err)
			os.Exit(1)
		}
	}

	progress := newCLIProgress()

	fmt.Printf("Decrypting %s\n", absInput)
	if absOut != "" {
		fmt.Printf("  Output: %s\n", absOut)
	}
	if *deleteEnc {
		fmt.Println("  Delete encrypted after decrypt: yes")
	}
	fmt.Println()

	err = wiiudownloader.DecryptContents(absInput, progress, *deleteEnc, absOut)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nDecryption failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Decryption complete.")
}

func parseHexTitleID(s string) (uint64, error) {
	var v uint64
	for i, c := range s {
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			return 0, fmt.Errorf("invalid hex char at position %d", i)
		}
		v = v<<4 | d
	}
	return v, nil
}
