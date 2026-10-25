package game

import (
	"errors"
	"strconv"
	"strings"
)

type Launch struct {
	Port, MaxPlayers                    int
	Map, Hostname, Password, RCON, GSLT string
}

func (p Profile) Command(l Launch) ([]string, error) {
	if l.Port < 1 || l.Port > 65535 {
		return nil, errors.New("invalid game port")
	}
	if l.Map == "" {
		l.Map = p.DefaultMap
	}
	if !safePart.MatchString(l.Map) {
		return nil, errors.New("invalid map name")
	}
	for _, s := range []string{l.Hostname, l.Password, l.RCON, l.GSLT} {
		if strings.ContainsAny(s, "\r\n\x00;\"\\") {
			return nil, errors.New("console control character in launch setting")
		}
	}
	if l.MaxPlayers < 0 || l.MaxPlayers > 128 {
		return nil, errors.New("invalid player limit")
	}
	args := []string{p.Executable, "-game", p.Dir, "-console", "-usercon", "-norestart", "-strictportbind", "-ip", "0.0.0.0", "-port", strconv.Itoa(l.Port), "+map", l.Map}
	if l.MaxPlayers > 0 {
		key := "-maxplayers"
		if p.Key == "csgo" {
			key = "-maxplayers_override"
		}
		args = append(args, key, strconv.Itoa(l.MaxPlayers))
	}
	if l.Hostname != "" {
		args = append(args, "+hostname", l.Hostname)
	}
	args = append(args, "+sv_password", l.Password)
	if l.RCON != "" {
		args = append(args, "+rcon_password", l.RCON)
	}
	if l.GSLT != "" {
		args = append(args, "+sv_setsteamaccount", l.GSLT)
	}
	return args, nil
}
