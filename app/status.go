package app

import (
	"time"

	domain "github.com/slidebolt/sb-domain"
)

func (a *App) healthyDevice(d domain.Device) domain.Device {
	d.Status = a.health("healthy", "")
	return d
}

func (a *App) healthyEntity(e domain.Entity) domain.Entity {
	e.Status = a.health("healthy", "")
	return e
}

func (a *App) health(state, message string) *domain.ResourceStatus {
	seconds := int((2 * a.config.Refresh).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return &domain.ResourceStatus{Health: state, ObservedAt: time.Now().UTC(), StaleAfterSeconds: seconds, Message: message}
}
