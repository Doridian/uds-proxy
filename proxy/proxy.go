/*
Package proxy implements an HTTP forward proxy that exclusively listens on a UNIX domain socket for
client requests. It uses a single http.Client to proxy requests, enabling connection
pooling. Optionally, the proxy can expose metrics via prometheus client library.
*/
package proxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"os/user"
	"runtime"
	"syscall"
	"time"

	"github.com/Doridian/peercred"
)

// AppVersion is set at compile time via make / ldflags
var AppVersion = "0.8.x-dev"

// Instance provides state storage for a single proxy instance.
type Instance struct {
	Options    Settings
	HTTPClient *http.Client
}

// Settings configure a Instance and need to be passed to NewProxyInstance().
type Settings struct {
	SocketPath          string
	SocketMode          int
	ClientTimeout       int
	MaxConnsPerHost     int
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	IdleConnTimeout     int
	SocketReadTimeout   int
	SocketWriteTimeout  int
	PrintVersion        bool
	NoLogTimeStamps     bool
	RemoteHTTPS         bool
	ForceRemoteHost     string
	InsecureSkipVerify  bool
}

// NewProxyInstance validates supplied Settings and returns a ready-to-run proxy instance.
func NewProxyInstance(args Settings) *Instance {
	if args.PrintVersion {
		println("uds-proxy", AppVersion, runtime.Version())
		os.Exit(0)
	}
	if args.SocketPath == "" {
		println("Error: -socket must be provided, use -h for help")
		os.Exit(1)
	}
	if args.NoLogTimeStamps {
		log.SetFlags(0)
	}
	log.Printf("👋 uds-proxy %s, pid %d starting...", AppVersion, os.Getpid())

	proxyInstance := Instance{}
	proxyInstance.Options = args
	proxyInstance.HTTPClient = newHTTPClient(&proxyInstance.Options)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go sigHandler(c, &proxyInstance)

	return &proxyInstance
}

// Run starts the proxy's socket server accept loop, which will run until Shutdown() is called.
func (proxy *Instance) Run() {
	proxy.startSocketServerAcceptLoop()
}

// Shutdown cleanly terminates a proxy instance (and is invoked by signal handlers or during tests).
func (proxy *Instance) Shutdown(sig os.Signal) {
	if sig == nil {
		sig = os.Interrupt
	}
	log.Printf("%v -- cleaning up", sig)
	proxy.HTTPClient.CloseIdleConnections()
	os.Remove(proxy.Options.SocketPath)
	log.Print("uds-proxy shut down cleanly. nice. good bye 👋")
}

func (proxy *Instance) startSocketServerAcceptLoop() {
	if _, err := os.Stat(proxy.Options.SocketPath); err == nil {
		err := os.Remove(proxy.Options.SocketPath)
		if err != nil {
			panic(err)
		}
	}

	server := http.Server{
		ReadTimeout:  time.Duration(proxy.Options.SocketReadTimeout) * time.Millisecond,
		WriteTimeout: time.Duration(proxy.Options.SocketWriteTimeout) * time.Millisecond,
		Handler:      http.HandlerFunc(proxy.handleProxyRequest),
		ConnContext:  ConnContext,
	}

	unixListener, err := net.Listen("unix", proxy.Options.SocketPath)
	if err != nil {
		panic(err)
	}
	err = os.Chmod(proxy.Options.SocketPath, os.FileMode(proxy.Options.SocketMode))
	if err != nil {
		panic(err)
	}
	server.Serve(unixListener)
}

func (proxy *Instance) handleProxyRequest(clientResponseWriter http.ResponseWriter, clientRequest *http.Request) {
	scheme := "http"
	if proxy.Options.RemoteHTTPS {
		scheme = "https"
	}

	targetHost := clientRequest.Host
	if proxy.Options.ForceRemoteHost != "" {
		targetHost = proxy.Options.ForceRemoteHost
	}

	targetURL := fmt.Sprintf("%s://%s%s", scheme, targetHost, clientRequest.URL)

	backendRequest, err := http.NewRequest(clientRequest.Method, targetURL, clientRequest.Body)
	if err != nil {
		http.Error(clientResponseWriter, err.Error(), http.StatusInternalServerError)
		return
	}
	backendRequest.Host = clientRequest.Host
	backendRequest.Header = clientRequest.Header

	conn := GetNetConn(clientRequest)
	cred, err := peercred.Read(conn.(*net.UnixConn))
	if err != nil {
		http.Error(clientResponseWriter, err.Error(), http.StatusInternalServerError)
		return
	}

	uidStr := fmt.Sprintf("%d", cred.UID)
	backendRequest.Header.Set("X-Auth-UID", uidStr)
	usr, err := user.LookupId(uidStr)
	if err == nil {
		backendRequest.Header.Set("X-Auth-User", usr.Username)
	} else {
		backendRequest.Header.Del("X-Auth-User")
	}

	gidStr := fmt.Sprintf("%d", cred.GID)
	backendRequest.Header.Set("X-Auth-GID", gidStr)
	group, err := user.LookupGroupId(gidStr)
	if err == nil {
		backendRequest.Header.Set("X-Auth-Group", group.Name)
	} else {
		backendRequest.Header.Del("X-Auth-Group")
	}

	backendRequest.Header.Del("X-Auth-Roles")
	backendRequest.Header.Set("X-Forwarded-For", "127.0.0.1")

	backendResponse, err := proxy.HTTPClient.Do(backendRequest)
	if err != nil {
		if err.(*url.Error).Timeout() {
			http.Error(clientResponseWriter, err.Error(), http.StatusGatewayTimeout)
		} else {
			http.Error(clientResponseWriter, err.Error(), http.StatusBadGateway)
		}
		return
	}

	for k, v := range backendResponse.Header {
		clientResponseWriter.Header().Set(k, v[0])
		for _, vv := range v[1:] {
			clientResponseWriter.Header().Add(k, vv)
		}
	}
	clientResponseWriter.WriteHeader(backendResponse.StatusCode)
	io.Copy(clientResponseWriter, backendResponse.Body)
	backendResponse.Body.Close()
}

func newHTTPClient(opt *Settings) (client *http.Client) {
	transport := http.Transport{
		MaxConnsPerHost:       opt.MaxConnsPerHost,
		MaxIdleConns:          opt.MaxIdleConns,
		MaxIdleConnsPerHost:   opt.MaxIdleConnsPerHost,
		IdleConnTimeout:       time.Duration(opt.IdleConnTimeout) * time.Millisecond,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Hour,
		ResponseHeaderTimeout: 1 * time.Hour,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: opt.InsecureSkipVerify},
	}
	client = &http.Client{
		Timeout:   time.Duration(opt.ClientTimeout) * time.Millisecond,
		Transport: &transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return
}
