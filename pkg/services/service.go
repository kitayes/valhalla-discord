package service

import (
	"context"
	"os"
	"os/signal"
)

type Logger interface {
	Error(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Info(format string, v ...interface{})
	Debug(format string, v ...interface{})
}

type (
	Service interface {
		Init() error
		Run(ctx context.Context)
		Stop()
	}
	Services interface {
		AddService(service ...Service)
		Run(ctx context.Context) error
	}
	Manager struct {
		log      Logger
		services []Service
	}
)

func NewManager(log Logger) Services {
	return &Manager{log: log}
}

func (s *Manager) AddService(service ...Service) {
	s.services = append(s.services, service...)
}

func (s *Manager) Run(ctx context.Context) error {
	var err error
	s.log.Info("going to start services")
	for count, service := range s.services {
		err = service.Init()
		if err != nil {
			for i := 0; i < count; i++ {
				service.Stop()
			}

			return err
		}
		go service.Run(ctx)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	select {
	case <-c:
		s.stop()
	case <-ctx.Done():
		s.stop()
	}

	return nil
}

func (s *Manager) stop() {
	s.log.Info("going to stop")
	for _, service := range s.services {
		service.Stop()
	}
}
