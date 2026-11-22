package game

import (
	"errors"
	"strconv"
	"strings"
)

type Launch struct {
	Port, MaxPlayers                    int
	Map, Hostname, Password, RCON, GSLT string
	CustomParams                        string
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
	extra, err := ParseParams(l.CustomParams)
	if err != nil {
		return nil, err
	}
	return append(args, extra...), nil
}

// ParseParams accepts quoted argv, never shell expansion or console separators.
// The engine identity, credentials, and wrapper controls remain authoritative.
func ParseParams(raw string) ([]string, error) {
	if len(raw) > 8192 || strings.ContainsAny(raw, "\r\n\x00;$`|&<>") {
		return nil, errors.New("CUSTOM_PARAMS exceeds bounds or contains shell/console controls")
	}
	var args []string
	var token strings.Builder
	var quote rune
	started, escaped := false, false
	flush := func() error {
		if !started {
			return nil
		}
		value := token.String()
		if len(value) > 1024 || strings.ContainsAny(value, "\"\\") {
			return errors.New("invalid CUSTOM_PARAMS argument")
		}
		key, _, _ := strings.Cut(strings.ToLower(value), "=")
		switch key {
		case "-game", "-port", "-ip", "+ip", "+hostport", "+map", "+hostname", "+sv_password", "+rcon_password", "+sv_setsteamaccount", "-maxplayers", "-maxplayers_override", "-console", "-usercon", "-norestart", "-strictportbind", "-binary", "-debug", "-debugger", "-debuglog", "-steam", "-steamcmd_script", "-steam_dir", "-steamcmd_dir", "-autoupdate", "-pidfile":
			return errors.New("CUSTOM_PARAMS cannot override managed launch settings")
		}
		args = append(args, value)
		if len(args) > 128 {
			return errors.New("too many CUSTOM_PARAMS arguments")
		}
		token.Reset()
		started = false
		return nil
	}
	for _, r := range raw {
		if escaped {
			token.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if r == ' ' || r == '\t' {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		started = true
		token.WriteRune(r)
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated quote or escape in CUSTOM_PARAMS")
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return args, nil
}
