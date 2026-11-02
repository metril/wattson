package api

import (
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

func TestAsyncAPIUpToDate(t *testing.T) {
	t.Parallel()
	got, err := AsyncAPIDoc()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../docs/asyncapi.yaml")
	if err != nil {
		t.Fatalf("read docs/asyncapi.yaml: %v; run: make openapi", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("docs/asyncapi.yaml is out of date; run: make openapi")
	}
}

type jsonObj = map[string]any

func asyncGaps(t *testing.T, raw []byte) (version string, gaps []string, messages []string) {
	t.Helper()
	var doc jsonObj
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	version, _ = doc["asyncapi"].(string)
	comp, _ := doc["components"].(jsonObj)
	schemas, _ := comp["schemas"].(jsonObj)
	msgs, _ := comp["messages"].(jsonObj)
	var walk func(path string, s jsonObj, seen map[string]bool)
	walk = func(path string, s jsonObj, seen map[string]bool) {
		if ref, ok := s["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			if seen[name] {
				return
			}
			target, ok := schemas[name].(jsonObj)
			if !ok {
				gaps = append(gaps, path+" -> unresolved "+ref)
				return
			}
			seen = copySeen(seen)
			seen[name] = true
			walk(path, target, seen)
			return
		}
		props, _ := s["properties"].(jsonObj)
		for name, p := range props {
			po, _ := p.(jsonObj)
			if d, _ := po["description"].(string); d == "" {
				gaps = append(gaps, path+"."+name)
			}
			walk(path+"."+name, po, copySeen(seen))
		}
		if it, ok := s["items"].(jsonObj); ok {
			walk(path+"[]", it, copySeen(seen))
		}
	}
	for name, m := range msgs {
		messages = append(messages, name)
		mo, _ := m.(jsonObj)
		if d, _ := mo["description"].(string); d == "" {
			gaps = append(gaps, "message "+name)
		}
		pl, _ := mo["payload"].(jsonObj)
		if pl == nil {
			gaps = append(gaps, "message "+name+" has no payload")
			continue
		}
		walk(name, pl, map[string]bool{})
	}
	sort.Strings(gaps)
	sort.Strings(messages)
	return
}

func copySeen(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestAsyncAPIDescribed(t *testing.T) {
	t.Parallel()
	raw, err := AsyncAPIDoc()
	if err != nil {
		t.Fatal(err)
	}
	version, gaps, messages := asyncGaps(t, raw)
	if version != "3.0.0" {
		t.Errorf("asyncapi = %q, want 3.0.0", version)
	}
	for _, g := range gaps {
		t.Errorf("undescribed: %s", g)
	}
	want := []string{"ack", "bike", "command", "error", "mqttPress", "mqttState", "mqttStatus", "state", "tick"}
	if strings.Join(messages, ",") != strings.Join(want, ",") {
		t.Errorf("messages = %v, want %v", messages, want)
	}
}

func TestAsyncAPIShape(t *testing.T) {
	t.Parallel()
	raw, err := AsyncAPIDoc()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Channels map[string]struct {
			Address string `json:"address"`
		} `json:"channels"`
		Servers map[string]struct {
			Host, Pathname, Protocol string
		} `json:"servers"`
		Operations map[string]struct{ Action string } `json:"operations"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Channels["ride"].Address != "/ws" {
		t.Errorf("channels = %+v", doc.Channels)
	}
	if len(doc.Servers) != 2 {
		t.Fatalf("servers = %+v", doc.Servers)
	}
	if s := doc.Servers["local"]; s.Protocol != "ws" || s.Host != "localhost:8080" || s.Pathname != "/ws" {
		t.Errorf("local server = %+v", s)
	}
	var send, recv int
	for _, o := range doc.Operations {
		switch o.Action {
		case "send":
			send++
		case "receive":
			recv++
		}
	}
	if send == 0 || recv != 2 { // the WebSocket command and the MQTT button
		t.Errorf("operations send=%d receive=%d", send, recv)
	}
	if !bytes.Contains(raw, []byte("wattson_session")) || !bytes.Contains(raw, []byte("bearer.")) {
		t.Error("security schemes do not describe the cookie and the bearer subprotocol")
	}
}

func TestAsyncAPIHasMQTT(t *testing.T) {
	t.Parallel()
	raw, err := AsyncAPIDoc()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Channels map[string]struct {
			Address    string
			Parameters map[string]struct{ Enum []string }
		}
		Servers map[string]struct{ Protocol, Host string }
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if s := doc.Servers["mqtt"]; s.Protocol != "mqtt" || s.Host != "broker:1883" {
		t.Errorf("mqtt server = %+v", s)
	}
	for _, topic := range []string{"wattson/status", "wattson/state", "wattson/command/{command}"} {
		if doc.Channels[topic].Address != topic {
			t.Errorf("channel %q = %+v", topic, doc.Channels[topic])
		}
	}
	if got := doc.Channels["wattson/command/{command}"].Parameters["command"].Enum; len(got) != 7 {
		t.Errorf("command enum = %v, want the 7 button keys", got)
	}
}
