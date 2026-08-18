package ilo

import (
	"encoding/json"
	"testing"
)

func TestLegacyRCInfoNormalize(t *testing.T) {
	var raw legacyRCInfo
	err := json.Unmarshal([]byte(`{"enc_key":"00112233445566778899aabbccddeeff","cmd_enc_key":"ffeeddccbbaa99887766554433221100","rc_port":17990,"vm_port":17988,"vm_key":"abc","protocol_version":1.3,"optional_features":"ENCRYPT_KEY;ENCRYPT_CMD;REMOTE_CLIPBOARD_SUPPORT"}`), &raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := raw.normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "legacy" || got.RCPort != 17990 || got.VMPort != 17988 || got.VMKey != "abc" || got.ProtocolVersion != 1 {
		t.Fatalf("unexpected rcinfo: %#v", got)
	}
	if got.LegacyKeyText != "00112233445566778899aabbccddeeff" || len(got.CommandKey) != 16 || got.CommandKey[0] != 0xff {
		t.Fatalf("legacy encryption data not preserved: %#v", got)
	}
	if !got.OptionalFeatures["ENCRYPT_KEY"] {
		t.Fatalf("features not parsed: %#v", got.OptionalFeatures)
	}
}

func TestLegacyRCInfoRejectsBadCommandKey(t *testing.T) {
	var raw legacyRCInfo
	if err := json.Unmarshal([]byte(`{"enc_key":"00112233445566778899aabbccddeeff","cmd_enc_key":"not-hex","rc_port":17990,"vm_port":17988}`), &raw); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.normalize(); err == nil {
		t.Fatal("expected bad cmd_enc_key error")
	}
}

func TestRedfishRCInfoNormalize(t *testing.T) {
	raw := redfishRCInfo{
		Enabled:         true,
		MasterKey:       "00112233445566778899aabbccddeeff",
		ProtocolVersion: 2,
		RCPort:          17990,
		VMPort:          17988,
	}
	got, err := raw.normalize()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Source != "redfish" || got.RCPort != 17990 || got.VMPort != 17988 || len(got.MasterKey) != 16 {
		t.Fatalf("unexpected rcinfo: %#v", got)
	}
}
