// Copyright 2018 Jigsaw Operations LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"fmt"
	"github.com/evgeniy-krivenko/outline-ss-server/pkg/consul"
	rpchandler "github.com/evgeniy-krivenko/outline-ss-server/rpc"
	"github.com/evgeniy-krivenko/outline-ss-server/server"
	"github.com/evgeniy-krivenko/vpn-api/gen/ss_service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healphpb "google.golang.org/grpc/health/grpc_health_v1"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/evgeniy-krivenko/outline-ss-server/service/metrics"
	"github.com/op/go-logging"
	"github.com/oschwald/geoip2-golang"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/ssh/terminal"
)

var logger *logging.Logger

// Set by goreleaser default ldflags. See https://goreleaser.com/customization/build/
var version = "dev"

// A UDP NAT timeout of at least 5 minutes is recommended in RFC 4787 Section 4.3.
const defaultNatTimeout = 5 * time.Minute

func init() {
	var prefix = "%{level:.1s}%{time:2006-01-02T15:04:05.000Z07:00} %{pid} %{shortfile}]"
	if terminal.IsTerminal(int(os.Stderr.Fd())) {
		// Add color only if the output is the terminal
		prefix = strings.Join([]string{"%{color}", prefix, "%{color:reset}"}, "")
	}
	logging.SetFormatter(logging.MustStringFormatter(strings.Join([]string{prefix, " %{message}"}, "")))
	logging.SetBackend(logging.NewLogBackend(os.Stderr, "", 0))
	logger = logging.MustGetLogger("")
}

func getHost() string {
	host, err := os.Hostname()
	if err != nil {
		host = "localhost"
	}

	return host
}

// RunSSServer starts a shadowsocks server running, and returns the server or an error.
func RunSSServer(filename string, natTimeout time.Duration, sm metrics.ShadowsocksMetrics, replayHistory int) (*server.SSServer, error) {
	cnf := &server.SSConfig{
		NatTimeout:    natTimeout,
		Metrics:       sm,
		ReplayHistory: replayHistory,
		Ports:         make(map[int]*server.SsPort),
		Logger:        logger,
	}
	srv := server.NewSSServer(cnf)
	err := srv.LoadConfig(filename)
	if err != nil {
		return nil, fmt.Errorf("Failed to load config file %v: %v", filename, err)
	}
	sigHup := make(chan os.Signal, 1)
	signal.Notify(sigHup, syscall.SIGHUP)
	go func() {
		for range sigHup {
			logger.Info("Updating config")
			if err := srv.LoadConfig(filename); err != nil {
				logger.Errorf("Could not reload config: %v", err)
			}
		}
	}()

	return srv, nil
}

func main() {
	var flags struct {
		ConfigFile    string
		MetricsAddr   string
		IPCountryDB   string
		natTimeout    time.Duration
		replayHistory int
		Verbose       bool
		Version       bool
		IsGRPC        bool
		IsConsul      bool
		GrpcPort      int
		GrpcAddress   string
		ServiceId     string
	}
	flag.StringVar(&flags.ConfigFile, "config", "", "Configuration filename")
	flag.StringVar(&flags.MetricsAddr, "metrics", "", "Address for the Prometheus metrics")
	flag.StringVar(&flags.IPCountryDB, "ip_country_db", "", "Path to the ip-to-country mmdb file")
	flag.DurationVar(&flags.natTimeout, "udptimeout", defaultNatTimeout, "UDP tunnel timeout")
	flag.IntVar(&flags.replayHistory, "replay_history", 0, "Replay buffer size (# of handshakes)")
	flag.BoolVar(&flags.Verbose, "verbose", false, "Enables verbose logging output")
	flag.BoolVar(&flags.Version, "version", false, "The version of the server")
	flag.BoolVar(&flags.IsGRPC, "grpc", false, "Should to start gRPC server")
	flag.BoolVar(&flags.IsConsul, "consul", false, "Should to start service discovery")
	flag.IntVar(&flags.GrpcPort, "grpc-port", 50051, "Port for gRPC service")
	flag.StringVar(&flags.GrpcAddress, "grpc-address", getHost(), "Address for gRPC service")
	flag.StringVar(&flags.ServiceId, "service-id", "", "Service id discovery")

	flag.Parse()

	if flags.Verbose {
		logging.SetLevel(logging.DEBUG, "")
	} else {
		logging.SetLevel(logging.INFO, "")
	}

	if flags.Version {
		fmt.Println(version)
		return
	}

	if flags.ConfigFile == "" {
		flag.Usage()
		return
	}

	if flags.MetricsAddr != "" {
		http.Handle("/metrics", promhttp.Handler())
		go func() {
			logger.Fatal(http.ListenAndServe(flags.MetricsAddr, nil))
		}()
		logger.Infof("Metrics on http://%v/metrics", flags.MetricsAddr)
	}

	var ipCountryDB *geoip2.Reader
	var err error
	if flags.IPCountryDB != "" {
		logger.Infof("Using IP-Country database at %v", flags.IPCountryDB)
		ipCountryDB, err = geoip2.Open(flags.IPCountryDB)
		if err != nil {
			log.Fatalf("Could not open geoip database at %v: %v", flags.IPCountryDB, err)
		}
		defer ipCountryDB.Close()
	}
	m := metrics.NewPrometheusShadowsocksMetrics(ipCountryDB, prometheus.DefaultRegisterer)
	m.SetBuildInfo(version)
	srv, err := RunSSServer(flags.ConfigFile, flags.natTimeout, m, flags.replayHistory)
	if err != nil {
		logger.Fatal(err)
	}

	if flags.IsGRPC {
		go func() {
			lis, err := net.Listen("tcp", fmt.Sprintf(":%d", flags.GrpcPort))
			if err != nil {
				panic(err)
			}
			defer func(lis net.Listener) {
				err := lis.Close()
				if err != nil {
					logger.Errorf("error close tcp conn")
				}
			}(lis)

			// inject SSServer to handler
			rpcSrv := rpchandler.NewGrpcHandler(srv)

			s := grpc.NewServer()
			ss_service.RegisterSsServiceServer(s, rpcSrv)

			hth := health.NewServer()
			healphpb.RegisterHealthServer(s, hth)

			logger.Infof("starting grpc server on port %d", flags.GrpcPort)
			if err := s.Serve(lis); err != nil {
				panic(err)
			}
		}()
	}

	if flags.IsConsul {
		c, err := consul.NewClient(fmt.Sprintf("%s:%d", "localhost", 8500))
		if err != nil {
			logger.Fatal(err)
		}
		err = c.GrpcRegistration(&consul.GrpcRegConf{
			Id:       flags.ServiceId,
			Name:     fmt.Sprintf("%s-ss", flags.ServiceId),
			Addr:     flags.GrpcAddress,
			Port:     flags.GrpcPort,
			Tags:     []string{"ss"},
			Interval: 30,
			TLS:      false,
		})
		if err != nil {
			logger.Fatal(err)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}
