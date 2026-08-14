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
		// Every channel is wrapped in RetryingSender (see retry.go) -- assert
		// against the unwrapped Inner sender, not New's own return type,
		// so this still proves New routes to the right underlying
		// implementation per channel type.
		retrying, ok := sender.(notifier.RetryingSender)
		require.True(t, ok, "expected New to wrap every channel in RetryingSender")
		assert.IsType(t, c.want, retrying.Inner)
	}
}

func TestNew_UnknownChannelType(t *testing.T) {
	_, err := notifier.New("carrier-pigeon")
	assert.ErrorContains(t, err, "unknown escalation channel type")
}

func TestNewForPolicy(t *testing.T) {
	t.Run("non-webhook channels ignore the template and delegate to New", func(t *testing.T) {
		sender, err := notifier.NewForPolicy("pagerduty", nil)
		require.NoError(t, err)
		retrying, ok := sender.(notifier.RetryingSender)
		require.True(t, ok)
		assert.IsType(t, notifier.PagerDutySender{}, retrying.Inner)
	})

	t.Run("webhook channel is also wrapped in RetryingSender, with the template applied", func(t *testing.T) {
		template := "{{title}}"
		sender, err := notifier.NewForPolicy("webhook", &template)
		require.NoError(t, err)
		retrying, ok := sender.(notifier.RetryingSender)
		require.True(t, ok, "expected NewForPolicy's webhook branch to be wrapped in RetryingSender too, not just New's")
		webhookSender, ok := retrying.Inner.(notifier.WebhookSender)
		require.True(t, ok)
		assert.Equal(t, template, webhookSender.Template)
	})
}
