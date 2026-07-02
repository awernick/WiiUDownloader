package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	wiiudownloader "github.com/Xpl0itU/WiiUDownloader"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const (
	NETWORK_DIAL_TIMEOUT         = 30 * time.Second
	NETWORK_DIAL_KEEP_ALIVE      = 30 * time.Second
	FALLBACK_DNS_DIAL_TIMEOUT    = 10 * time.Second
	HTTP_MAX_IDLE_CONNS          = 100
	HTTP_MAX_IDLE_CONNS_PER_HOST = 100
	HTTP_MAX_CONNS_PER_HOST      = 100
	HTTP_IDLE_CONN_TIMEOUT       = 90 * time.Second
	HTTP_TLS_HANDSHAKE_TIMEOUT   = 10 * time.Second
	HTTP_RESPONSE_HEADER_TIMEOUT = 10 * time.Second
	HTTP_EXPECT_CONTINUE_TIMEOUT = 1 * time.Second
	// Used only as DNS fallback when system resolver fails.
	FALLBACK_DNS_RESOLVER_ENDPOINT = "1.1.1.1:53"
)

func main() {
	runtime.LockOSThread()
	runtime.GOMAXPROCS(runtime.NumCPU())

	config, configErr := loadConfig()
	if config == nil {
		config = getDefaultConfig()
	}
	if runtime.GOOS == "darwin" {
		if config.DarkMode {
			os.Setenv("GTK_THEME", "Adwaita:dark")
		} else {
			os.Setenv("GTK_THEME", "Adwaita")
		}
	}

	configureMacOSEnvironment()
	gtk.Init(nil)

	setDarkTheme(config.DarkMode)

	app, err := gtk.ApplicationNew("io.github.xpl0itu.wiiudownloader", glib.APPLICATION_FLAGS_NONE)
	if err != nil {
		showFatalDialogAndLog("Error creating application", err)
		return
	}

	if runtime.GOOS == "darwin" {
		quitAction := glib.SimpleActionNew("quit", nil)
		quitAction.Connect("activate", func() {
			app.Quit()
		})
		app.AddAction(quitAction)
		app.SetAccelsForAction("app.quit", []string{"<Primary>q"})
	}

	client := buildHTTPClient()
	if configErr != nil {
		log.Printf("error loading config: %v", configErr)
		errorDialog := gtk.MessageDialogNew(nil, 0, gtk.MESSAGE_WARNING, gtk.BUTTONS_OK, "Error loading config: %v\n\nStarting with default settings.", configErr)
		errorDialog.Run()
		errorDialog.Destroy()
	}

	win := NewMainWindow(wiiudownloader.GetTitleEntries(wiiudownloader.TITLE_CATEGORY_GAME), client, config)
	config.saveConfigCallback = func() {
		uiIdleAdd(func() {
			win.applyConfig(config)
		})
	}

	app.Connect("activate", func(app *gtk.Application) {
		if !config.DidInitialSetup {
			assistant, err := NewInitialSetupAssistantWindow(config)
			if err != nil {
				showFatalDialogAndLog("Error creating setup assistant", err)
				return
			}
			assistant.SetPostSetupCallback(func() {
				showMainWindow(app, win)
			})
			assistant.assistantWindow.ShowAll()
			app.AddWindow(assistant.assistantWindow)
			if win.window != nil {
				win.window.Hide()
			}
			return
		}

		showMainWindow(app, win)
	})

	app.Run(os.Args)
}

func configureMacOSEnvironment() {
	if runtime.GOOS != "darwin" {
		return
	}
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("error getting executable path: %v", err)
		return
	}
	if filepath.Base(filepath.Dir(execPath)) != "MacOS" {
		return
	}

	bundlePath := filepath.Dir(filepath.Dir(execPath))
	os.Unsetenv("DYLD_LIBRARY_PATH")
	os.Unsetenv("DYLD_FALLBACK_LIBRARY_PATH")
	os.Unsetenv("DYLD_INSERT_LIBRARIES")
	os.Unsetenv("PKG_CONFIG_PATH")

	glibPath := filepath.Join(bundlePath, "Resources", "share", "glib-2.0", "schemas")
	if _, err := os.Stat(glibPath); err == nil {
		os.Setenv("GSETTINGS_SCHEMA_DIR", glibPath)
	}

	loaderDir := filepath.Join(bundlePath, "MacOS", "lib", "gdkpixbuf_loaders")
	if _, err := os.Stat(loaderDir); err == nil {
		os.Setenv("GDK_PIXBUF_MODULE_DIR", loaderDir)

		if cachePath, ok := bundledLoadersCachePath(bundlePath); ok {
			cacheData, err := os.ReadFile(cachePath)
			if err == nil && strings.Contains(string(cacheData), "libpixbufloader_svg") {
				os.Setenv("GDK_PIXBUF_MODULE_FILE", cachePath)
				log.Printf("Set GDK_PIXBUF_MODULE_FILE to bundled cache %s", cachePath)
			} else {
				log.Printf("Bundled cache %s missing SVG loader, regenerating", cachePath)
				regenerateLoadersCache(loaderDir)
			}
		} else {
			log.Printf("Bundled loaders cache not found, falling back to runtime generation from %s", loaderDir)
			regenerateLoadersCache(loaderDir)
		}
	} else {
		log.Printf("LoaderDir not found: %s", loaderDir)
	}

	gioModPath := filepath.Join(bundlePath, "MacOS", "lib", "gio-modules")
	os.Setenv("GIO_MODULE_DIR", gioModPath)
	os.Setenv("GIO_EXTRA_MODULES", gioModPath)

	sharePath := filepath.Join(bundlePath, "Resources", "share")
	if _, err := os.Stat(sharePath); err == nil {
		os.Setenv("XDG_DATA_DIRS", sharePath)
	}
}

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
						d := net.Dialer{Timeout: FALLBACK_DNS_DIAL_TIMEOUT}
						return d.DialContext(ctx, "udp", FALLBACK_DNS_RESOLVER_ENDPOINT)
					},
				}
				host, port, splitErr := net.SplitHostPort(addr)
				if splitErr != nil {
					return nil, err
				}
				ips, lookupErr := resolver.LookupIPAddr(ctx, host)
				if lookupErr != nil {
					log.Printf("fallback DNS lookup failed: %v", lookupErr)
					return nil, err
				}
				if len(ips) == 0 {
					return nil, err
				}
				targetAddr := net.JoinHostPort(ips[0].String(), port)
				return dialer.DialContext(ctx, network, targetAddr)
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

func showMainWindow(app *gtk.Application, win *MainWindow) {
	win.SetApplicationForGTKWindow(app)
	win.BuildUI()
	app.AddWindow(win.window)
	if win.window != nil {
		win.window.Show()
	}
}

func showFatalDialogAndLog(prefix string, err error) {
	log.Printf("%s: %v", prefix, err)
	d := gtk.MessageDialogNew(nil, 0, gtk.MESSAGE_ERROR, gtk.BUTTONS_OK, "%s: %v", prefix, err)
	d.Run()
	d.Destroy()
}

func bundledLoadersCachePath(bundlePath string) (string, bool) {
	cachePath := filepath.Join(bundlePath, "Resources", "loaders.cache")
	if _, err := os.Stat(cachePath); err == nil {
		return cachePath, true
	}
	return "", false
}

func regenerateLoadersCache(loaderDir string) {
	cacheDir, _ := os.UserCacheDir()
	cachePath := filepath.Join(cacheDir, "wiiu-loaders.cache")
	loaders, _ := filepath.Glob(filepath.Join(loaderDir, "*.so"))
	cacheData := buildLoadersCache(loaders)
	if err := os.WriteFile(cachePath, cacheData, 0o644); err == nil {
		os.Setenv("GDK_PIXBUF_MODULE_FILE", cachePath)
		log.Printf("Set GDK_PIXBUF_MODULE_FILE to regenerated %s", cachePath)
	} else {
		log.Printf("Failed to write regenerated cache: %v", err)
	}
}

func buildLoadersCache(loaders []string) []byte {
	var cache bytes.Buffer
	cache.WriteString("# GdkPixbuf Image Loader Modules\n# Automatically generated\n\n")
	for _, loader := range loaders {
		filename := filepath.Base(loader)
		entry := getLoaderEntry(filename, loader)
		if entry == "" {
			continue
		}
		cache.WriteString(entry)
		cache.WriteString("\n")
	}
	return cache.Bytes()
}

func getLoaderEntry(filename, path string) string {
	switch {
	case strings.Contains(filename, "svg"):
		return fmt.Sprintf("%q\n\"svg\" 6 \"gdk-pixbuf\" \"Scalable Vector Graphics\" \"LGPL\"\n\"image/svg+xml\" \"image/svg\" \"image/svg-xml\" \"image/vnd.adobe.svg+xml\" \"text/xml-svg\" \"image/svg+xml-compressed\" \"\"\n\"svg\" \"svgz\" \"svg.gz\" \"\"\n\" <svg\" \"*    \" 100\n\" <!DOCTYPE svg\" \"*             \" 100", path)
	case strings.Contains(filename, "bmp"):
		return fmt.Sprintf("%q\n\"bmp\" 5 \"gdk-pixbuf\" \"BMP\" \"LGPL\"\n\"image/bmp\" \"image/x-bmp\" \"image/x-MS-bmp\" \"\"\n\"bmp\" \"\"\n\"BM\" \"\" 100", path)
	case strings.Contains(filename, "gif"):
		return fmt.Sprintf("%q\n\"gif\" 4 \"gdk-pixbuf\" \"GIF\" \"LGPL\"\n\"image/gif\" \"\"\n\"gif\" \"\"\n\"GIF8\" \"\" 100", path)
	case strings.Contains(filename, "ico"):
		return fmt.Sprintf("%q\n\"ico\" 5 \"gdk-pixbuf\" \"Windows icon\" \"LGPL\"\n\"image/x-icon\" \"image/x-ico\" \"image/x-win-bitmap\" \"image/vnd.microsoft.icon\" \"application/ico\" \"image/ico\" \"image/icon\" \"text/ico\" \"\"\n\"ico\" \"cur\" \"\"\n\"  \\001   \" \"zz znz\" 100\n\"  \\002   \" \"zz znz\" 100", path)
	default:
		return ""
	}
}
