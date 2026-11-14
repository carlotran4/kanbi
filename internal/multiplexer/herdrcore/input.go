package herdrcore

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/carlotran4/kanbi/internal/multiplexer"
)

// SendText is a literal paste, with no implicit Enter, shell, or hc send queue.
// Attach at the existing size; never resize the target to the board's dimensions.
func (a *Adapter) SendText(ctx context.Context, r multiplexer.ContainerRef, text string) error {
	t, err := a.get(ctx, r)
	if err != nil {
		return err
	}
	if len(text) > 1<<20 {
		return errors.New("core paste exceeds 1 MiB")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := a.command(ctx, a.Config.Binary, "connect")
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	defer func() { in.Close(); cmd.Process.Kill(); cmd.Wait() }()
	if _, err = io.WriteString(in, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"hello\",\"params\":{\"framed\":true,\"generation\":1}}\n"); err != nil {
		return err
	}
	reader := bufio.NewReader(out)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	var h hello
	if err = decodeReply(line, &h); err != nil {
		return err
	}
	var m metadata
	_ = json.Unmarshal([]byte(r.Metadata), &m)
	if h.Generation != 1 || h.Core.ID != m.CoreID {
		return errors.New("unsupported core generation")
	}
	write := func(ch uint32, kind byte, payload []byte) error {
		header := make([]byte, 9)
		binary.LittleEndian.PutUint32(header, ch)
		header[4] = kind
		binary.LittleEndian.PutUint32(header[5:], uint32(len(payload)))
		if _, e := in.Write(header); e != nil {
			return e
		}
		_, e := in.Write(payload)
		return e
	}
	read := func() (uint32, byte, []byte, error) {
		header := make([]byte, 9)
		if _, e := io.ReadFull(reader, header); e != nil {
			return 0, 0, nil, e
		}
		n := binary.LittleEndian.Uint32(header[5:])
		if n > 16<<20 {
			return 0, 0, nil, errors.New("oversized core frame")
		}
		p := make([]byte, n)
		_, e := io.ReadFull(reader, p)
		return binary.LittleEndian.Uint32(header), header[4], p, e
	}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "terminal.attach", "params": map[string]any{"terminal": r.ID, "cols": t.Cols, "rows": t.Rows}})
	if err = write(0, 0, raw); err != nil {
		return err
	}
	var channel uint32
	for {
		ch, k, p, e := read()
		if e != nil {
			return e
		}
		if ch == 0 && k == 0 {
			var v struct {
				Channel  uint32   `json:"channel"`
				Terminal terminal `json:"terminal"`
			}
			if e = decodeReply(p, &v); e != nil {
				return e
			}
			if v.Terminal.Address != r.ID || v.Terminal.PID != m.PID || v.Terminal.Created != m.Created || v.Terminal.Labels["kanbi.owner"] != m.Token || v.Terminal.State != "live" || v.Terminal.Reason != nil {
				return multiplexer.ErrContainerNotFound
			}
			channel = v.Channel
			break
		}
	}
	if channel == 0 {
		return errors.New("core attach returned invalid channel")
	}
	if err = write(channel, 0, []byte("\x1b[200~"+text+"\x1b[201~")); err != nil {
		return err
	}
	if err = write(channel, 2, nil); err != nil {
		return err
	}
	for {
		ch, k, p, e := read()
		if e != nil {
			return e
		}
		if ch == channel && k == 2 {
			if len(p) != 0 {
				return errors.New("core program exited during paste")
			}
			return nil
		}
		if ch == channel && k == 0 {
			credit := make([]byte, 4)
			binary.LittleEndian.PutUint32(credit, uint32(len(p)))
			if e = write(channel, 1, credit); e != nil {
				return e
			}
		}
	}
}
