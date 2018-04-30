package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env"
)

var version = "unknown"
var date = "unknown"
var cnf config

type config struct {
	Port                  int      `env:"PORT" envDefault:"80"`
	ClickhouseServers     []string `env:"CLICKHOUSE_SERVERS" envSeparator:","`
	ClickhouseDownTimeout int      `env:"CLICKHOUSE_DOWN_TIMEOUT" envDefault:"300"`
	FlushCount            int      `env:"FLUSH_COUNT" envDefault:"10000"`
	FlushInterval         int      `env:"FLUSH_INTERVAL" envDefault:"1000"`
	DumpDir               string   `env:"DUMP_DIR" envDefault:"dumps"`
	Debug                 bool     `env:"DEBUG" envDefault:"false"`
}

// SafeQuit - safe prepare to quit
func SafeQuit(collect *Collector, sender Sender) {
	collect.FlushAll()
	if count := sender.Len(); count > 0 {
		log.Printf("Sending %+v tables\n", count)
	}
	for !sender.Empty() && !collect.Empty() {
		collect.WaitFlush()
	}
	collect.WaitFlush()
}

func failOnError(err error) {
	if err != nil {
		log.Fatalf("%s", err)
	}
}

func main() {

	log.SetOutput(os.Stdout)

	flag.Parse()

	if flag.Arg(0) == "version" {
		log.Printf("clickhouse-bulk ver. %+v (%+v)\n", version, date)
		return
	}

	err := env.Parse(&cnf)
	failOnError(err)

	dumper := new(FileDumper)
	dumper.Path = cnf.DumpDir
	sender := NewClickhouse(cnf.ClickhouseDownTimeout)
	sender.Dumper = dumper
	for _, url := range cnf.ClickhouseServers {
		sender.AddServer(url)
	}

	collect := NewCollector(sender, cnf.FlushCount, cnf.FlushInterval)

	// send collected data on SIGTERM and exit
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	srv := InitServer(fmt.Sprintf(":%d", cnf.Port), collect, cnf.Debug)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		for {
			_ = <-signals
			log.Printf("STOP signal\n")
			if err := srv.Shutdown(ctx); err != nil {
				log.Printf("Shutdown error %+v\n", err)
				SafeQuit(collect, sender)
				os.Exit(1)
			}
		}
	}()

	err = srv.Start()
	if err != nil {
		log.Printf("ListenAndServe: %+v\n", err)
		SafeQuit(collect, sender)
		os.Exit(1)
	}
}
