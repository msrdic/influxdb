package main

import (
	"configuration"
	"coordinator"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"server"
	"strconv"
	"time"

	"github.com/jmhodges/levigo"

	log "code.google.com/p/log4go"
)

func setupLogging(loggingLevel, logFile string) {
	level := log.DEBUG
	switch loggingLevel {
	case "info":
		level = log.INFO
	case "warn":
		level = log.WARNING
	case "error":
		level = log.ERROR
	}

	for _, filter := range log.Global {
		filter.Level = level
	}

	if logFile == "stdout" {
		flw := log.NewConsoleLogWriter()
		log.AddFilter("stdout", level, flw)

	} else {
		logFileDir := filepath.Dir(logFile)
		os.MkdirAll(logFileDir, 0744)

		flw := log.NewFileLogWriter(logFile, false)
		log.AddFilter("file", level, flw)

		flw.SetFormat("[%D %T] [%L] (%S) %M")
		flw.SetRotate(true)
		flw.SetRotateSize(0)
		flw.SetRotateLines(0)
		flw.SetRotateDaily(true)
	}

	log.Info("Redirectoring logging to %s", logFile)
}

func main() {
	fileName := flag.String("config", "config.toml.sample", "Config file")
	wantsVersion := flag.Bool("v", false, "Get version number")
	resetRootPassword := flag.Bool("reset-root", false, "Reset root password")
	pidFile := flag.String("pidfile", "", "the pid file")
	repairLeveldb := flag.Bool("repair-ldb", false, "set to true to repair the leveldb files")

	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()

	if wantsVersion != nil && *wantsVersion {
		fmt.Printf("InfluxDB v%s (git: %s) (leveldb: %d.%d)\n", version, gitSha, levigo.GetLevelDBMajorVersion(), levigo.GetLevelDBMinorVersion())
		return
	}
	config := configuration.LoadConfiguration(*fileName)
	setupLogging(config.LogLevel, config.LogFile)

	if *repairLeveldb {
		log.Info("Repairing leveldb")
		files, err := ioutil.ReadDir(config.DataDir)
		if err != nil {
			panic(err)
		}
		o := levigo.NewOptions()
		defer o.Close()
		for _, f := range files {
			p := path.Join(config.DataDir, f.Name())
			log.Info("Repairing %s", p)
			if err := levigo.RepairDatabase(p, o); err != nil {
				panic(err)
			}
		}
	}

	if pidFile != nil && *pidFile != "" {
		pid := strconv.Itoa(os.Getpid())
		if err := ioutil.WriteFile(*pidFile, []byte(pid), 0644); err != nil {
			panic(err)
		}
	}

	if config.BindAddress == "" {
		log.Info("Starting Influx Server...")
	} else {
		log.Info("Starting Influx Server bound to %s ...", config.BindAddress)
	}
	log.Info(`
+---------------------------------------------+
|  _____        __ _            _____  ____   |
| |_   _|      / _| |          |  __ \|  _ \  |
|   | |  _ __ | |_| |_   ___  _| |  | | |_) | |
|   | | | '_ \|  _| | | | \ \/ / |  | |  _ <  |
|  _| |_| | | | | | | |_| |>  <| |__| | |_) | |
| |_____|_| |_|_| |_|\__,_/_/\_\_____/|____/  |
+---------------------------------------------+

`)
	os.MkdirAll(config.RaftDir, 0744)
	os.MkdirAll(config.DataDir, 0744)
	server, err := server.NewServer(config)
	if err != nil {
		// sleep for the log to flush
		time.Sleep(time.Second)
		panic(err)
	}

	if err := startProfiler(server); err != nil {
		panic(err)
	}

	if *resetRootPassword {
		// TODO: make this not suck
		// This is ghetto as hell, but it'll work for now.
		go func() {
			time.Sleep(2 * time.Second) // wait for the raft server to join the cluster

			log.Warn("Resetting root's password to %s", coordinator.DEFAULT_ROOT_PWD)
			if err := server.RaftServer.CreateRootUser(); err != nil {
				panic(err)
			}
		}()
	}
	err = server.ListenAndServe()
	if err != nil {
		log.Error("ListenAndServe failed: ", err)
	}
}
