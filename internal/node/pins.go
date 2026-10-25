package node

import (
	"errors"
	"fmt"
	"github.com/edgegamers/source-egg/internal/content"
	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
	"os"
)

func (d *Daemon) desired(s Server) (content.Generation, error) {
	d.mu.Lock()
	key := d.pins["server:"+s.UUID]
	if key == "" {
		key = d.pins["game:"+s.Identity.Key()]
	}
	d.mu.Unlock()
	if key != "" {
		g, err := d.Store.Generation(s.Identity, key)
		if err != nil {
			return g, fmt.Errorf("pinned generation unavailable (will not use latest): %v", err)
		}
		return g, nil
	}
	return d.Store.Current(s.Identity)
}
func (d *Daemon) savePins() error {
	root, err := os.OpenRoot(d.Config.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return fsx.JSON(root, "pins.json", d.pins, 0600)
}
func (d *Daemon) pinServer(uuid, key string) error {
	d.prepare.Lock()
	defer d.prepare.Unlock()
	d.mu.Lock()
	server, ok := d.servers[uuid]
	d.mu.Unlock()
	if !ok {
		return errors.New("unknown server")
	}
	if key != "" {
		if _, err := d.Store.Generation(server.Identity, key); err != nil {
			return err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if key == "" {
		delete(d.pins, "server:"+uuid)
	} else {
		d.pins["server:"+uuid] = key
	}
	return d.savePins()
}
func (d *Daemon) pinGame(id game.Identity, key string) error {
	d.prepare.Lock()
	defer d.prepare.Unlock()
	if err := id.Validate(); err != nil {
		return err
	}
	if key != "" {
		if _, err := d.Store.Generation(id, key); err != nil {
			return err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if key == "" {
		delete(d.pins, "game:"+id.Key())
	} else {
		d.pins["game:"+id.Key()] = key
	}
	return d.savePins()
}
