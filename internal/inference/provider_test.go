package inference

import (
	"errors"
	"testing"
)

func TestRequestValidate(t *testing.T) {
	user := []Message{{Role: RoleUser, Content: "hi"}}

	tests := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{"valid single user message", Request{Model: "m", Messages: user}, false},
		{"all roles", Request{Model: "m", Messages: []Message{
			{Role: RoleSystem}, {Role: RoleUser}, {Role: RoleAssistant},
		}}, false},
		{"empty content is allowed", Request{Model: "m", Messages: []Message{{Role: RoleUser}}}, false},
		{"missing model", Request{Messages: user}, true},
		{"nil messages", Request{Model: "m"}, true},
		{"empty messages", Request{Model: "m", Messages: []Message{}}, true},
		{"unknown role", Request{Model: "m", Messages: []Message{{Role: "tool", Content: "x"}}}, true},
		{"empty role", Request{Model: "m", Messages: []Message{{Content: "x"}}}, true},
		{"one bad role among good ones", Request{Model: "m", Messages: []Message{
			{Role: RoleUser, Content: "x"}, {Role: "bot", Content: "y"},
		}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("error %v does not wrap ErrInvalidRequest", err)
			}
		})
	}
}
