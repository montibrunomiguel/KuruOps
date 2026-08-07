package notifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/notifier"
)

func TestNew(t *testing.T) {
	cases := []struct {
		channelType string
		want        any
	}{
		{"pagerduty", notifier.PagerDutySender{}},
		{"slack", notifier.SlackSender{}},
		{"webhook", notifier.WebhookSender{}},
	}
	for _, c := range cases {
		sender, err := notifier.New(c.channelType)
		require.NoError(t, err)
		assert.IsType(t, c.want, sender)
	}
}

func TestNew_UnknownChannelType(t *testing.T) {
	_, err := notifier.New("carrier-pigeon")
	assert.ErrorContains(t, err, "unknown escalation channel type")
}
