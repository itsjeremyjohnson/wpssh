package ssh

import "testing"

func TestConnectionKeySeparatesSSHIdentities(t *testing.T) {
	base := ClientConfig{Host: "example.com", Port: 22, User: "alice", IdentityFile: "/keys/alice"}
	key := connectionKey(base, "192.0.2.1:22")
	for _, other := range []ClientConfig{
		{Host: "example.com", Port: 22, User: "bob", IdentityFile: "/keys/alice"},
		{Host: "example.com", Port: 22, User: "alice", IdentityFile: "/keys/bob"},
		{Host: "other.example.com", Port: 22, User: "alice", IdentityFile: "/keys/alice"},
		{Host: "example.com", Port: 22, User: "alice", IdentityFile: "/keys/alice", Passphrase: "other"},
		{Host: "example.com", Port: 22, User: "alice", IdentityFile: "/keys/alice", ForwardAgent: true},
	} {
		if connectionKey(other, "192.0.2.1:22") == key {
			t.Fatalf("distinct SSH identity shared pool key: %+v", other)
		}
	}
	if connectionKey(base, "192.0.2.1:22") != key {
		t.Fatal("same SSH identity did not reuse pool key")
	}
}
