package server

import (
	"github.com/berryons/log"
	"google.golang.org/grpc"
	"net"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
)

var (
	supportedNetworks = []string{"unix", "tcp"}
)

func New(
	network, address string,
	unaryServerInterceptors []grpc.UnaryServerInterceptor,
	streamServerInterceptors []grpc.StreamServerInterceptor,
) *GrpcServer {

	// Check Network
	checkNetwork(network, address)

	// Network Listener 생성.
	listener, err := net.Listen(strings.ToLower(network), address)
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
		listener: listener,
		Server:   grpcServer,
		network:  network,
		address:  address,
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

type GrpcServer struct {
	listener net.Listener
	Server   *grpc.Server
	network  string
	address  string
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

	log.Printf("Start gRPC server on %s, %s\n", pSelf.network, pSelf.address)
	// Network Listener 에 등록 된 Handler 에 들어오는 연결을 수락하고,
	// gRPC Service Handler 와 연결하는 새 연결을 생성하여 요청을 Handler 에 전달.
	if err := pSelf.Server.Serve(pSelf.listener); err != nil {
		log.Fatalf("Failed to serve: %v\n", err)
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
