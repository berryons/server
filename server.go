package server

import (
	"context"
	"fmt"
	"github.com/berryons/log"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
)

var (
	supportedNetworks = []string{"unix", "tcp"}
)

type HttpProxyServerHandler func(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) (err error)

func New(
	network, address string,
	port int,
	unaryServerInterceptors []grpc.UnaryServerInterceptor,
	streamServerInterceptors []grpc.StreamServerInterceptor,
) *GrpcServer {
	fullAddress := fmt.Sprintf("%s:%d", address, port)

	// Check Network
	checkNetwork(network, fullAddress)

	// Network Listener 생성.
	listener, err := net.Listen(strings.ToLower(network), fullAddress)
	if err != nil {
		log.Fatalf("Failed to listen: %v\n", err)
	}

	// Server options
	var serverOptions []grpc.ServerOption
	if len(unaryServerInterceptors) > 0 {
		serverOptions = append(serverOptions, grpc.ChainUnaryInterceptor(unaryServerInterceptors...))
	}
	if len(streamServerInterceptors) > 0 {
		serverOptions = append(serverOptions, grpc.ChainStreamInterceptor(streamServerInterceptors...))
	}

	// gRPC Server 생성.
	grpcServer := grpc.NewServer(serverOptions...)

	return &GrpcServer{
		listener:      listener,
		Server:        grpcServer,
		network:       network,
		address:       address,
		port:          port,
		httpProxyMux:  nil,
		httpProxyPort: -1,
	}
}

func checkNetwork(network, address string) {
	if len(network) == 0 || len(address) == 0 {
		log.Fatal("Server network or address environment variable not set.")
	}

	if !slices.Contains(supportedNetworks, network) {
		log.Fatalf("This network is not supported: %s\n", network)
	}
}

type Server interface {
	Run()
	RegisterHttpProxyServer(httpProxyServerHandlerFuncSlice []HttpProxyServerHandler, ctx context.Context, mux *runtime.ServeMux, opts []grpc.DialOption, httpProxyPort int)
}

type GrpcServer struct {
	listener net.Listener
	Server   *grpc.Server
	network  string
	address  string
	port     int

	httpProxyMux  *runtime.ServeMux
	httpProxyPort int
}

func (pSelf *GrpcServer) Run() {

	if pSelf.Server == nil {
		log.Fatal("gRPC Server is nil...")
	}

	// signal handler
	cSig := make(chan os.Signal, 1)
	signal.Notify(cSig, os.Interrupt, os.Kill, syscall.SIGTERM)

	// Run shut down Goroutine
	go pSelf.postDestroy(cSig)

	// gRPC Gateway (Http Proxy) 실행.
	if pSelf.httpProxyMux != nil && pSelf.port != pSelf.httpProxyPort && pSelf.httpProxyPort > 0 {
		go pSelf.runHttpProxy()
	}

	log.Printf("Start gRPC server on %s, %s\n", pSelf.network, fmt.Sprintf("%s:%d", pSelf.address, pSelf.port))
	// Network Listener 에 등록 된 Handler 에 들어오는 연결을 수락하고,
	// gRPC Service Handler 와 연결하는 새 연결을 생성하여 요청을 Handler 에 전달.
	if err := pSelf.Server.Serve(pSelf.listener); err != nil {
		log.Fatalf("Failed to serve: %v\n", err)
	}
}

func (pSelf *GrpcServer) runHttpProxy() {
	if pSelf.httpProxyMux == nil || pSelf.httpProxyPort == -1 {
		log.Println("Http Proxy Server is not set")
		return
	}

	proxyFullAddress := fmt.Sprintf("%s:%d", pSelf.address, pSelf.httpProxyPort)
	log.Printf("Start HTTP proxy server on %s, %s\n", pSelf.network, proxyFullAddress)
	if err := http.ListenAndServe(proxyFullAddress, pSelf.httpProxyMux); err != nil {
		log.Fatalf("failed to listen and serve Http proxy server: %v", err)
	}
}

func (pSelf *GrpcServer) RegisterHttpProxyServer(httpProxyServerHandlerFuncSlice []HttpProxyServerHandler, ctx context.Context, mux *runtime.ServeMux, opts []grpc.DialOption, httpProxyPort int) {
	if httpProxyServerHandlerFuncSlice == nil || len(httpProxyServerHandlerFuncSlice) == 0 {
		log.Fatal("Http Proxy Server is nil...")
	}

	pSelf.httpProxyPort = httpProxyPort
	if pSelf.httpProxyPort == -1 {
		pSelf.httpProxyPort = pSelf.port + 1
	}

	checkedCtx := ctx
	checkedMux := mux
	checkedOptions := opts

	if checkedCtx == nil {
		checkedCtx = context.Background()
	}

	if checkedMux == nil {
		checkedMux = runtime.NewServeMux()
	}
	pSelf.httpProxyMux = mux

	if checkedOptions == nil {
		checkedOptions = []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}
	}

	for _, httpProxyServerHandlerFunc := range httpProxyServerHandlerFuncSlice {
		if err := httpProxyServerHandlerFunc(checkedCtx, checkedMux, fmt.Sprintf("%s:%d", pSelf.address, pSelf.port), checkedOptions); err != nil {
			log.Fatalf("failed to register Http gateway: %v (%v)", err, &httpProxyServerHandlerFunc)
		}
	}
}

func (pSelf *GrpcServer) postDestroy(cSig chan os.Signal) {
	sig := <-cSig
	log.Printf("Caught signal: %s", sig)
	log.Println("Shutting down the server...")

	err := pSelf.listener.Close()
	if err != nil {
		log.Fatal(err)
	}

	if strings.EqualFold("unix", pSelf.network) {
		err = os.Remove(pSelf.address)
		if err != nil {
			log.Fatal(err)
		}
	}

	log.Println("Bye Bye!!!")
	os.Exit(0)
}
