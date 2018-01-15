// Copyright (c) 2017 The VolantMQ Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/VolantMQ/persistence-boltdb"
	"github.com/VolantMQ/volantmq"
	"github.com/VolantMQ/volantmq/auth"
	"github.com/VolantMQ/volantmq/configuration"
	"github.com/VolantMQ/volantmq/transport"
	"github.com/spf13/viper"
	"go.uber.org/zap"

	_ "net/http/pprof"
	_ "runtime/debug"
	"fmt"
)

func main() {
	viper.SetConfigName("config_main")
	viper.AddConfigPath("conf")
	viper.SetConfigType("toml")
	if err := viper.ReadInConfig(); err != nil {
		panic(fmt.Sprintf("Couldn't read config file: %v", err))
		os.Exit(1)
	}

	ops := configuration.Options{
		LogWithTs: true,
		LogLevel: viper.GetString("log.level"),
		LogEnableTrace: viper.GetBool("log.enable_trace"),
	}

	configuration.Init(ops)

	logger := configuration.GetLogger().Named("example")

	var err error

	logger.Info("Starting application")
	logger.Info("Allocated cores", zap.Int("GOMAXPROCS", runtime.GOMAXPROCS(0)))

	go func() {
		http.ListenAndServe("localhost:6061", nil) // nolint: errcheck
	}()

	logger.Info("Initializing configs")


	ia := internalAuth{
		creds: make(map[string]string),
	}

	var internalCreds []struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}

	if err = viper.UnmarshalKey("mqtt.auth.internal", &internalCreds); err != nil {
		logger.Error("Couldn't unmarshal config", zap.Error(err))
		os.Exit(1)
	}

	for i := range internalCreds {
		ia.creds[internalCreds[i].User] = internalCreds[i].Password
	}

	if err = auth.Register("internal", ia); err != nil {
		logger.Error("Couldn't register *internal* auth provider", zap.Error(err))
		os.Exit(1)
	}

	var srv volantmq.Server

	listenerStatus := func(id string, status string) {
		logger.Info("Listener status", zap.String("id", id), zap.String("status", status))
	}

	serverConfig := volantmq.NewServerConfig()
	serverConfig.OfflineQoS0 = true
	serverConfig.TransportStatus = listenerStatus
	serverConfig.AllowDuplicates = false
	serverConfig.Authenticators = "internal"
	serverConfig.AllowOverlappingSubscriptions = true

	serverConfig.Persistence, err = boltdb.New(&boltdb.Config{
		File: "./persist.db",
	})

	if err != nil {
		logger.Error("Couldn't init BoltDB persistence", zap.Error(err))
		os.Exit(1)
	}

	srv, err = volantmq.NewServer(serverConfig)

	if err != nil {
		logger.Error("Couldn't create server", zap.Error(err))
		os.Exit(1)
	}

	var authMng *auth.Manager

	if authMng, err = auth.NewManager("internal"); err != nil {
		logger.Error("Couldn't register *amqp* auth provider", zap.Error(err))
		return
	}


	transportConfig := &transport.Config{
		Port:        viper.GetString("mqtt.tcp.port"),
		AuthManager: authMng,
	}
	if transportConfig.Port == "" {
		transportConfig.Port = "1883"
	}
	config := transport.NewConfigTCP(transportConfig)


	var tcpSSLEnabled = viper.GetBool("mqtt.tcp.ssl_enable")
	if tcpSSLEnabled {
		config.CertFile = viper.GetString("mqtt.tcp.ssl_cert_file")
		config.KeyFile = viper.GetString("mqtt.tcp.ssl_cert_key_file")
		if config.CertFile == "" {
			logger.Error("tcp ssl enabled, but ssl cert file is not set !")
			return
		}

		if config.KeyFile == "" {
			logger.Error("tcp ssl enabled, but ssl cert key file is not set !")
			return
		}
	}

	if err = srv.ListenAndServe(config); err != nil {
		logger.Error("Couldn't start listener", zap.Error(err))
		return
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	sig := <-ch
	logger.Info("Received signal...starting shutdown...", zap.String("signal", sig.String()))
	if err = srv.Close(); err != nil {
		logger.Error("Couldn't shutdown server", zap.Error(err))
	} else {
		logger.Info("Server shut down.")
	}



	//os.Remove("./persist.db") // nolint: errcheck
}
