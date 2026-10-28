package supervisor

import "strings"

// Buffering whole lines prevents a partial reserved command from being echoed
// or executed by the child. The limit also bounds memory for unterminated input.
const maxPanelLine = 256 << 10

type panelInput struct {
	server  *Server
	partial []byte
	discard bool
	afterCR bool
}

func (p *panelInput) feed(input []byte) error {
	for _, c := range input {
		if c == '\n' && p.afterCR {
			p.afterCR = false
			continue
		}
		p.afterCR = c == '\r'
		if c == '\r' || c == '\n' {
			if !p.discard {
				if err := p.server.panelLine(string(p.partial)); err != nil {
					return err
				}
			}
			p.partial = p.partial[:0]
			p.discard = false
			continue
		}
		if p.discard {
			continue
		}
		if len(p.partial) == maxPanelLine {
			p.partial = p.partial[:0]
			p.discard = true
			if p.server.Line != nil {
				p.server.Line("SourceEgg | WARNING | Console command exceeded 256 KiB and was discarded")
			}
			continue
		}
		p.partial = append(p.partial, c)
	}
	return nil
}

func (s *Server) panelLine(raw string) error {
	line := strings.TrimSpace(raw)
	if s.InterceptInput != nil && s.InterceptInput(line) {
		return nil
	}
	if line != "" && s.OnInput != nil {
		s.OnInput(line)
	}
	return s.Send(raw)
}
