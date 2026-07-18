package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"hpeirc/internal/app"
	"hpeirc/internal/ilo"
	"hpeirc/internal/keyboardmap"
	"hpeirc/internal/login"
)

func main() {
	var cfg app.Config
	var lang string
	flag.StringVar(&cfg.Addr, "addr", "", "iLO address, optionally address:https_port")
	flag.StringVar(&cfg.User, "name", "", "iLO user name")
	flag.StringVar(&cfg.Password, "password", "", "iLO password")
	flag.StringVar(&cfg.ISOPath, "iso", "", "local ISO file to mount as virtual CD/DVD media")
	flag.StringVar(&lang, "lang", "en", "accepted for HPLOCONS CLI compatibility")
	flag.BoolVar(&cfg.Share, "share", false, "request shared console when iLO reports the console is busy")
	flag.BoolVar(&cfg.Seize, "seize", false, "seize console when iLO reports the console is busy")
	flag.BoolVar(&cfg.VerifyCert, "verify-cert", false, "verify the iLO HTTPS certificate")
	flag.BoolVar(&cfg.Debug, "debug", false, "write verbose protocol/input status to hpeirc-debug.log")
	flag.StringVar(&cfg.LogPath, "log", "", "verbose log file path; enables logging even without -debug")
	flag.Parse()
	executablePath, executableErr := os.Executable()
	if executableErr != nil {
		executablePath = os.Args[0]
	}
	keyboardMaps := keyboardmap.LoadForExecutable(executablePath)
	cfg.KeyboardMaps = keyboardMaps.Registry
	cfg.KeyboardMapDir = keyboardMaps.Directory
	cfg.KeyboardMapWarnings = keyboardMaps.Warnings
	if executableErr != nil {
		cfg.KeyboardMapWarnings = append(cfg.KeyboardMapWarnings, fmt.Sprintf("Executable path could not be resolved exactly: %v", executableErr))
	}

	_ = lang
	if cfg.Share && cfg.Seize {
		log.Fatal("-share and -seize are mutually exclusive")
	}
	if cfg.Addr == "" || cfg.User == "" || cfg.Password == "" {
		base := cfg
		sessions := map[string]*app.SessionWindow{}
		err := login.RunLauncher(context.Background(), login.Fields{
			Addr: cfg.Addr,
			User: cfg.User,
		}, func(fields login.Fields) error {
			key := fields.IdentityKey()
			if session := sessions[key]; session != nil {
				session.Focus()
				return nil
			}
			sessionCfg := base
			sessionCfg.Addr = fields.Addr
			sessionCfg.User = fields.User
			sessionCfg.Password = fields.Password
			session, err := app.OpenSession(context.Background(), sessionCfg, func() {
				delete(sessions, key)
			})
			if err != nil {
				return err
			}
			sessions[key] = session
			session.Focus()
			return nil
		})
		if err != nil {
			log.Fatal(err)
		}
		for _, session := range sessions {
			session.Close()
		}
		return
	}

	if cfg.Addr != "" {
		host, _, err := ilo.ParseAddress(cfg.Addr)
		if err != nil {
			log.Fatal(err)
		}
		if strings.TrimSpace(host) == "" {
			log.Fatal("-addr must contain a host")
		}
	}

	if err := app.Run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
