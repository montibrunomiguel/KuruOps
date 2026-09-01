package domain_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
)

func participant(name string) domain.OnCallParticipant {
	return domain.OnCallParticipant{UserID: uuid.New(), UserName: name}
}

func names(set []domain.OnCallParticipant) []string {
	out := make([]string, len(set))
	for i, p := range set {
		out[i] = p.UserName
	}
	return out
}

func TestResolveOnCallSet_NoParticipants(t *testing.T) {
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	got := domain.ResolveOnCallSet(nil, handover, 7, 1, domain.OnCallWorkingHoursAllDay, nil, nil, handover.Add(time.Hour))
	assert.Empty(t, got)
}

func TestResolveOnCallSet_BeforeHandover(t *testing.T) {
	alice := participant("Alice")
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	got := domain.ResolveOnCallSet([]domain.OnCallParticipant{alice}, handover, 7, 1, domain.OnCallWorkingHoursAllDay, nil, nil, handover.Add(-time.Minute))
	assert.Empty(t, got, "the rotation hasn't started yet")
}

func TestResolveOnCallSet_SingleParticipantAlwaysOn(t *testing.T) {
	alice := participant("Alice")
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	got := domain.ResolveOnCallSet([]domain.OnCallParticipant{alice}, handover, 7, 1, domain.OnCallWorkingHoursAllDay, nil, nil, handover.AddDate(0, 0, 30))
	assert.Equal(t, []string{"Alice"}, names(got))
}

func TestResolveOnCallSet_WeeklyRotationAlternates(t *testing.T) {
	alice, bob := participant("Alice"), participant("Bob")
	participants := []domain.OnCallParticipant{alice, bob}
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		now  time.Time
		want []string
	}{
		{"period 0 -- exactly at handover", handover, []string{"Alice"}},
		{"period 0 -- mid-week", handover.AddDate(0, 0, 3), []string{"Alice"}},
		{"period 1 -- one week later", handover.AddDate(0, 0, 7), []string{"Bob"}},
		{"period 2 -- two weeks later, wraps back to Alice", handover.AddDate(0, 0, 14), []string{"Alice"}},
		{"period 5 -- odd period is Bob", handover.AddDate(0, 0, 35), []string{"Bob"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := domain.ResolveOnCallSet(participants, handover, 7, 1, domain.OnCallWorkingHoursAllDay, nil, nil, c.now)
			assert.Equal(t, c.want, names(got))
		})
	}
}

func TestResolveOnCallSet_DailyCadence(t *testing.T) {
	alice, bob := participant("Alice"), participant("Bob")
	participants := []domain.OnCallParticipant{alice, bob}
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	assert.Equal(t, []string{"Alice"}, names(domain.ResolveOnCallSet(participants, handover, 1, 1, domain.OnCallWorkingHoursAllDay, nil, nil, handover.AddDate(0, 0, 2))))
	assert.Equal(t, []string{"Bob"}, names(domain.ResolveOnCallSet(participants, handover, 1, 1, domain.OnCallWorkingHoursAllDay, nil, nil, handover.AddDate(0, 0, 3))))
}

func TestResolveOnCallSet_CustomCadence(t *testing.T) {
	alice, bob := participant("Alice"), participant("Bob")
	participants := []domain.OnCallParticipant{alice, bob}
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	// period_days = 3: period 0 is days [0,3), period 1 is [3,6), etc.
	assert.Equal(t, []string{"Alice"}, names(domain.ResolveOnCallSet(participants, handover, 3, 1, domain.OnCallWorkingHoursAllDay, nil, nil, handover.AddDate(0, 0, 2))))
	assert.Equal(t, []string{"Bob"}, names(domain.ResolveOnCallSet(participants, handover, 3, 1, domain.OnCallWorkingHoursAllDay, nil, nil, handover.AddDate(0, 0, 3))))
}

func TestResolveOnCallSet_ConcurrentShiftsEvenSplit(t *testing.T) {
	chris, sam, willis, tom := participant("Chris"), participant("SamStarling"), participant("SamWillis"), participant("Tom")
	participants := []domain.OnCallParticipant{chris, sam, willis, tom}
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	t.Run("period 0 -- first pair", func(t *testing.T) {
		got := domain.ResolveOnCallSet(participants, handover, 7, 2, domain.OnCallWorkingHoursAllDay, nil, nil, handover)
		assert.Equal(t, []string{"Chris", "SamStarling"}, names(got))
	})
	t.Run("period 1 -- second pair", func(t *testing.T) {
		got := domain.ResolveOnCallSet(participants, handover, 7, 2, domain.OnCallWorkingHoursAllDay, nil, nil, handover.AddDate(0, 0, 7))
		assert.Equal(t, []string{"SamWillis", "Tom"}, names(got))
	})
	t.Run("period 2 -- wraps back to the first pair", func(t *testing.T) {
		got := domain.ResolveOnCallSet(participants, handover, 7, 2, domain.OnCallWorkingHoursAllDay, nil, nil, handover.AddDate(0, 0, 14))
		assert.Equal(t, []string{"Chris", "SamStarling"}, names(got))
	})
}

func TestResolveOnCallSet_ConcurrentShiftsUnevenSplit(t *testing.T) {
	// 3 participants, concurrency 2. The last group wraps back to the start
	// -- [Alice Bob] then [Carol Alice] -- so every period is staffed by the
	// two the schedule promises. Truncating instead (the old behaviour) left
	// Carol on call alone every third period, which a schedule configured
	// for two analysts never advertised.
	alice, bob, carol := participant("Alice"), participant("Bob"), participant("Carol")
	participants := []domain.OnCallParticipant{alice, bob, carol}
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	at := func(days int) []string {
		return names(domain.ResolveOnCallSet(participants, handover, 7, 2, domain.OnCallWorkingHoursAllDay, nil, nil, handover.AddDate(0, 0, days)))
	}
	assert.Equal(t, []string{"Alice", "Bob"}, at(0))
	assert.Equal(t, []string{"Carol", "Alice"}, at(7), "the wrap keeps the period at full strength")
	assert.Equal(t, []string{"Alice", "Bob"}, at(14), "and the cycle repeats")

	// Every period is fully staffed, which is the whole point.
	for _, days := range []int{0, 7, 14, 21, 28} {
		assert.Len(t, at(days), 2, "period at day %d must have both slots filled", days)
	}
}

func TestRotationShortfall(t *testing.T) {
	t.Run("an evenly divided roster reports no shortfall", func(t *testing.T) {
		got := domain.RotationShortfall(4, 2)
		assert.False(t, got.Uneven)
		assert.Equal(t, 0, got.Doubled)
		assert.Equal(t, 2, got.Groups)
	})

	t.Run("an uneven roster reports how many double up", func(t *testing.T) {
		// 5 responders, 2 slots -> groups [0,1] [2,3] [4,0]: one person
		// (participant 0) serves two periods back to back per cycle.
		got := domain.RotationShortfall(5, 2)
		assert.True(t, got.Uneven)
		assert.Equal(t, 1, got.Doubled)
		assert.Equal(t, 3, got.Groups)
	})

	t.Run("a single responder is never uneven", func(t *testing.T) {
		assert.False(t, domain.RotationShortfall(1, 1).Uneven)
	})

	t.Run("more slots than people is clamped, not uneven", func(t *testing.T) {
		assert.False(t, domain.RotationShortfall(2, 5).Uneven)
	})

	t.Run("an empty roster is not a shortfall, just empty", func(t *testing.T) {
		assert.False(t, domain.RotationShortfall(0, 2).Uneven)
	})
}

func TestResolveOnCallSet_ConcurrencyClampedToParticipantCount(t *testing.T) {
	alice := participant("Alice")
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	got := domain.ResolveOnCallSet([]domain.OnCallParticipant{alice}, handover, 7, 5, domain.OnCallWorkingHoursAllDay, nil, nil, handover)
	assert.Equal(t, []string{"Alice"}, names(got), "concurrency greater than participant count doesn't panic or duplicate")
}

func TestResolveOnCallSet_WorkingHoursGap(t *testing.T) {
	alice := participant("Alice")
	handover := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC) // a Monday
	businessHours := []domain.OnCallWorkingHoursInterval{
		{Weekdays: []int{1, 2, 3, 4, 5}, StartMinute: 9 * 60, EndMinute: 17 * 60}, // Mon-Fri 09:00-17:00
	}

	t.Run("within business hours -- on call", func(t *testing.T) {
		now := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC) // Monday 10:00
		got := domain.ResolveOnCallSet([]domain.OnCallParticipant{alice}, handover, 7, 1, domain.OnCallWorkingHoursSpecificTimes, businessHours, nil, now)
		assert.Equal(t, []string{"Alice"}, names(got))
	})
	t.Run("outside business hours -- gap, nobody on call", func(t *testing.T) {
		now := time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC) // Monday 20:00
		got := domain.ResolveOnCallSet([]domain.OnCallParticipant{alice}, handover, 7, 1, domain.OnCallWorkingHoursSpecificTimes, businessHours, nil, now)
		assert.Empty(t, got)
	})
	t.Run("weekend -- gap, nobody on call", func(t *testing.T) {
		now := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC) // Saturday 10:00
		got := domain.ResolveOnCallSet([]domain.OnCallParticipant{alice}, handover, 7, 1, domain.OnCallWorkingHoursSpecificTimes, businessHours, nil, now)
		assert.Empty(t, got)
	})
}

func TestResolveOnCallSet_OverrideWinsOutright(t *testing.T) {
	alice, bob := participant("Alice"), participant("Bob")
	override := participant("Carol")
	handover := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	got := domain.ResolveOnCallSet([]domain.OnCallParticipant{alice, bob}, handover, 7, 1, domain.OnCallWorkingHoursAllDay, nil, &override, handover)
	assert.Equal(t, []string{"Carol"}, names(got), "an override replaces the whole computed set, even though Alice would otherwise be on call")
}

func TestResolveOnCallSet_OverrideWinsEvenDuringAGap(t *testing.T) {
	alice := participant("Alice")
	override := participant("Carol")
	handover := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	businessHours := []domain.OnCallWorkingHoursInterval{{Weekdays: []int{1}, StartMinute: 9 * 60, EndMinute: 17 * 60}}
	outsideHours := time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC)

	got := domain.ResolveOnCallSet([]domain.OnCallParticipant{alice}, handover, 7, 1, domain.OnCallWorkingHoursSpecificTimes, businessHours, &override, outsideHours)
	assert.Equal(t, []string{"Carol"}, names(got))
}

type onCallRotationFixture struct {
	Name             string                              `json:"name"`
	Participants     []domain.OnCallParticipant          `json:"participants"`
	HandoverAt       time.Time                           `json:"handoverAt"`
	PeriodDays       int                                 `json:"periodDays"`
	ConcurrentShifts int                                 `json:"concurrentShifts"`
	WorkingHoursMode domain.OnCallWorkingHoursMode       `json:"workingHoursMode"`
	WorkingHours     []domain.OnCallWorkingHoursInterval `json:"workingHours"`
	Override         *domain.OnCallParticipant           `json:"override"`
	LocalNow         time.Time                           `json:"localNow"`
	ExpectedUserIDs  []string                            `json:"expectedUserIds"`
}

// TestResolveOnCallSet_CrossLanguageFixtures reads
// docs/oncall-rotation-fixtures.json -- the same file
// frontend/src/lib/onCallRotation.test.ts reads -- so a handful of concrete
// cases are asserted identical against both the Go original and its
// hand-maintained TS port, cheap insurance against silent drift between
// the two.
func TestResolveOnCallSet_CrossLanguageFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/oncall-rotation-fixtures.json")
	require.NoError(t, err)

	var fixtures []onCallRotationFixture
	require.NoError(t, json.Unmarshal(raw, &fixtures))
	require.NotEmpty(t, fixtures)

	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			got := domain.ResolveOnCallSet(
				f.Participants, f.HandoverAt, f.PeriodDays, f.ConcurrentShifts,
				f.WorkingHoursMode, f.WorkingHours, f.Override, f.LocalNow,
			)
			gotIDs := make([]string, len(got))
			for i, p := range got {
				gotIDs[i] = p.UserID.String()
			}
			assert.Equal(t, f.ExpectedUserIDs, gotIDs)
		})
	}
}
