package link

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/socketwire"
)

func TestSocketAttachHelloAndRequest(t *testing.T) {
	dir, _ := os.MkdirTemp("/tmp", "lks")
	defer os.RemoveAll(dir)
	svc := NewService()
	svc.Handle(contract.OpPing, func(context.Context, *Request) (any, error) { return map[string]string{"pong": "1"}, nil })
	srv, err := NewSocketServer(filepath.Join(dir, "gateway.sock"), svc, func(ctx context.Context, id, tr string) (Principal, error) {
		return Principal{Principal: contract.Principal{OwnerID: "owner", DeviceID: id, Class: contract.ClassPhone}, Transport: tr}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	c, err := net.Dial("unix", filepath.Join(dir, "gateway.sock"))
	if err != nil {
		t.Fatal(err)
	}
	st := socketwire.NewStream(c)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, _ := json.Marshal(socketwire.Attach{DeviceID: "phone-1", Transport: "iscp"})
	if err := st.WriteFrame(ctx, b); err != nil {
		t.Fatal(err)
	}
	raw, err := st.ReadFrame(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var res socketwire.AttachResult
	_ = json.Unmarshal(raw, &res)
	if !res.OK {
		t.Fatal(string(raw))
	}
	hello, _ := contract.Encode(contract.Frame{Type: contract.FrameHello, ID: "hello", Hello: &contract.Hello{Versions: []string{contract.Version}, Device: &contract.Device{Class: contract.ClassPhone}}})
	if err := st.WriteFrame(ctx, hello); err != nil {
		t.Fatal(err)
	}
	raw, err = st.ReadFrame(ctx)
	if err != nil {
		t.Fatal("no hello reply: ", err)
	}
	f, err := contract.Decode(raw)
	if err != nil || f.Type != contract.FrameHello {
		t.Fatalf("hello reply %s %v", raw, err)
	}
	req, _ := contract.Request("r1", contract.OpPing, nil)
	rb, _ := contract.Encode(req)
	_ = st.WriteFrame(ctx, rb)
	raw, err = st.ReadFrame(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f, _ = contract.Decode(raw)
	if f.Type != contract.FrameResponse || f.OK == nil || !*f.OK {
		t.Fatalf("ping %s", raw)
	}
}
