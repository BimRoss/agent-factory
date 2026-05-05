package runtime

import "testing"

func TestIsCreateIssueCapability(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{in: "create-issue", want: true},
		{in: "create-github-issue", want: true},
		{in: "CREATE-GITHUB-ISSUE", want: true},
		{in: "read-github", want: false},
	}

	for _, tc := range tests {
		if got := isCreateIssueCapability(tc.in); got != tc.want {
			t.Fatalf("isCreateIssueCapability(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
