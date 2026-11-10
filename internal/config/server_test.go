// Tests the listener configuration: default port and rejection of invalid ones.
package config

import "testing"

func TestServerPort(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		want    string
		wantErr bool
	}{
		{"default", "", ":8090", false},
		{"custom", "8080", ":8080", false},
		{"lowest", "1", ":1", false},
		{"highest", "65535", ":65535", false},
		{"not a number", "http", "", true},
		{"zero", "0", "", true},
		{"too high", "65536", "", true},
		{"negative", "-1", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(portKey, tt.env)

			got, err := loadServer()

			if (err != nil) != tt.wantErr || (!tt.wantErr && got.Addr() != tt.want) {
				t.Errorf("got (%q, %v), want %q (error %v)", got.Addr(), err, tt.want, tt.wantErr)
			}
		})
	}
}
