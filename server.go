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
	httpProxyHandler HttpProxyServerHandler,
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
		listener:         listener,
		Server:           grpcServer,
		network:          network,
		address:          address,
		port:             port,
		httpProxyHandler: httpProxyHandler,
		proxyPort:        port + 1,
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
	RunHttpProxy()
	RegisterHttpProxyServerHandler(f HttpProxyServerHandler)
	RegisterHttpProxyServerHandlerWithOption(f HttpProxyServerHandler, ctx context.Context, mux *runtime.ServeMux, opts []grpc.DialOption)
}

type GrpcServer struct {
	listener net.Listener
	Server   *grpc.Server
	network  string
	address  string
	port     int

	httpProxyHandler HttpProxyServerHandler
	proxyPort        int
	// TODO: Credentials
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

	log.Printf("Start gRPC server on %s, %s\n", pSelf.network, fmt.Sprintf("%s:%d", pSelf.address, pSelf.port))
	// Network Listener 에 등록 된 Handler 에 들어오는 연결을 수락하고,
	// gRPC Service Handler 와 연결하는 새 연결을 생성하여 요청을 Handler 에 전달.
	if err := pSelf.Server.Serve(pSelf.listener); err != nil {
		log.Fatalf("Failed to serve: %v\n", err)
	}
}

func (pSelf *GrpcServer) RunHttpProxy() {
	proxyFullAddress := fmt.Sprintf("%s:%d", pSelf.address, pSelf.proxyPort)
	ctx := context.Background()
	mux := runtime.NewServeMux()
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	if err := pSelf.httpProxyHandler(ctx, mux, proxyFullAddress, options); err != nil {
		log.Fatalf("failed to register Http proxy: %v", err)
	}

	log.Printf("Start HTTP proxy server on %s, %s\n", pSelf.network, proxyFullAddress)

	if err := http.ListenAndServe(proxyFullAddress, mux); err != nil {
		log.Fatalf("failed to listen and serve Http proxy server: %v", err)
	}
}

func (pSelf *GrpcServer) RegisterHttpProxyServerHandler(f HttpProxyServerHandler) {
	pSelf.RegisterHttpProxyServerHandlerWithOption(f, nil, nil, nil)
}

func (pSelf *GrpcServer) RegisterHttpProxyServerHandlerWithOption(f HttpProxyServerHandler, ctx context.Context, mux *runtime.ServeMux, opts []grpc.DialOption) {
	checkedCtx := ctx
	checkedMux := mux
	checkedOptions := opts

	if checkedCtx == nil {
		checkedCtx = context.Background()
	}

	if checkedMux == nil {
		checkedMux = runtime.NewServeMux()
	}

	if checkedOptions == nil {
		checkedOptions = []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}
	}

	if err := f(checkedCtx, checkedMux, fmt.Sprintf("%s:%d", pSelf.address, pSelf.port), checkedOptions); err != nil {
		log.Fatalf("failed to register Http gateway: %v", err)
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
