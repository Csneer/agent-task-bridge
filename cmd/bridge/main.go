// Command bridge runs the single-authority M1 reference daemon.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"agent-task-bridge/bridge"
	"agent-task-bridge/internal/wirevalue"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("bridge: %v", err)
		os.Exit(1)
	}
}

func privateFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("key and config files must be regular files accessible only by their owner (0600)")
	}
	return io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
}

func loadKey(path string, initialize bool) ([]byte, error) {
	if initialize {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err == nil {
			_, err = f.Write(key)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return nil, err
		}
	}
	key, err := privateFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("key must contain exactly 32 raw bytes")
	}
	return key, nil
}

func listenPolicy(address, cert, key string) error {
	if (cert == "") != (key == "") {
		return errors.New("both TLS certificate and key are required")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if cert == "" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("cleartext listening requires a numeric loopback address; use TLS for other addresses")
	}
	return nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("bridge", flag.ContinueOnError)
	database := fs.String("db", "runtime/bridge.sqlite", "persistent authority database")
	keyPath := fs.String("key", "runtime/bridge.key", "external persistent 32-byte key (0600)")
	initKey := fs.Bool("init-key", false, "create a new key exclusively; never replace an existing key")
	configPath := fs.String("config", "", "trusted config to import; omit on normal restart")
	importOnly := fs.Bool("import-only", false, "import config and exit; stop the daemon first")
	listen := fs.String("listen", "127.0.0.1:8080", "HTTP/TLS listen address")
	cert := fs.String("tls-cert", "", "TLS certificate file")
	tlsKey := fs.String("tls-key", "", "TLS key file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if err := listenPolicy(*listen, *cert, *tlsKey); err != nil {
		return err
	}
	if *importOnly && *configPath == "" {
		return errors.New("import-only requires config")
	}
	if err := os.MkdirAll(filepath.Dir(*database), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(*database+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("authority database is already in use by another daemon or importer")
	}
	key, err := loadKey(*keyPath, *initKey)
	if err != nil {
		return err
	}
	var cfg *bridge.Config
	if *configPath != "" {
		raw, err := privateFile(*configPath)
		if err != nil {
			return err
		}
		cfg = &bridge.Config{}
		if len(raw) > 4*1024*1024 {
			return errors.New("configuration exceeds 4 MiB")
		}
		if _, err = wirevalue.Parse(raw); err != nil {
			return errors.New("invalid configuration JSON")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(cfg); err != nil {
			return errors.New("invalid configuration JSON")
		}
	}
	service, err := bridge.Open(*database, cfg, bridge.Options{Key: key})
	if err != nil {
		return err
	}
	defer service.Close()
	if *importOnly {
		fmt.Println("configuration imported")
		return nil
	}
	server := &http.Server{Addr: *listen, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	failures := make(chan error, 1)
	go func() {
		if *cert != "" {
			failures <- server.ServeTLS(listener, *cert, *tlsKey)
		} else {
			failures <- server.Serve(listener)
		}
	}()
	log.Printf("bridge ready: authority=%s address=%s", service.AuthorityID(), listener.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err = <-failures:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
