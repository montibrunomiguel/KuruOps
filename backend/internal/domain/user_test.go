package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/argusops/argusops/internal/domain"
)

func TestResourceAccess_Has(t *testing.T) {
	cases := []struct {
		name string
		ra   domain.ResourceAccess
		cap  string
		want bool
	}{
		{"present", domain.ResourceAccess{"alerts", "incidents"}, "alerts", true},
		{"absent", domain.ResourceAccess{"alerts"}, "incidents", false},
		{"empty set", domain.ResourceAccess{}, "alerts", false},
		{"nil set", nil, "alerts", false},
		{"followup granted independently", domain.ResourceAccess{"followup"}, "incidents", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.ra.Has(c.cap))
		})
	}
}

func TestValidateResourceAccess(t *testing.T) {
	t.Run("all three known capabilities are valid, individually and combined", func(t *testing.T) {
		assert.NoError(t, domain.ValidateResourceAccess(domain.ResourceAccess{"alerts"}))
		assert.NoError(t, domain.ValidateResourceAccess(domain.ResourceAccess{"incidents"}))
		assert.NoError(t, domain.ValidateResourceAccess(domain.ResourceAccess{"followup"}))
		assert.NoError(t, domain.ValidateResourceAccess(domain.ResourceAccess{"alerts", "incidents", "followup"}))
	})

	t.Run("empty set is valid (no access granted)", func(t *testing.T) {
		assert.NoError(t, domain.ValidateResourceAccess(domain.ResourceAccess{}))
		assert.NoError(t, domain.ValidateResourceAccess(nil))
	})

	t.Run("an unknown capability is rejected", func(t *testing.T) {
		err := domain.ValidateResourceAccess(domain.ResourceAccess{"alerts", "bogus"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "bogus")
	})

	t.Run("the old enum value 'both' is no longer valid", func(t *testing.T) {
		err := domain.ValidateResourceAccess(domain.ResourceAccess{"both"})
		assert.Error(t, err)
	})
}

func TestValidatePhone(t *testing.T) {
	cases := []struct {
		name    string
		phone   string
		wantErr bool
	}{
		{"empty is valid (optional field)", "", false},
		{"valid E.164 with country code", "+5511912345678", false},
		{"valid, shorter number", "+15550199", false},
		{"missing leading +", "5511912345678", true},
		{"leading zero after +", "+05511912345678", true},
		{"contains spaces", "+55 11 91234-5678", true},
		{"contains a dash", "+1-555-0199", true},
		{"too short", "+551", true},
		{"letters", "+55abc12345", true},
		{"just a plus sign", "+", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := domain.ValidatePhone(c.phone)
			if c.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
