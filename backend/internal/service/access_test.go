package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
)

func TestTagsVisible(t *testing.T) {
	t.Run("empty allowedTags means everything is visible, regardless of the record's own tags", func(t *testing.T) {
		assert.True(t, tagsVisible(nil, []string{"finance", "pci"}))
		assert.True(t, tagsVisible([]string{}, nil))
		assert.True(t, tagsVisible(nil, nil))
	})

	t.Run("a record with no tags is invisible once allowedTags is non-empty", func(t *testing.T) {
		assert.False(t, tagsVisible([]string{"finance"}, nil))
		assert.False(t, tagsVisible([]string{"finance"}, []string{}))
	})

	t.Run("disjoint tag sets are not visible", func(t *testing.T) {
		assert.False(t, tagsVisible([]string{"finance"}, []string{"hr", "legal"}))
	})

	t.Run("any single overlapping tag makes the record visible", func(t *testing.T) {
		assert.True(t, tagsVisible([]string{"finance", "hr"}, []string{"legal", "hr"}))
	})

	t.Run("full overlap is visible", func(t *testing.T) {
		assert.True(t, tagsVisible([]string{"finance", "pci"}, []string{"finance", "pci"}))
	})
}

func TestLatestAnalysisFields(t *testing.T) {
	t.Run("nil run yields all-nil fields", func(t *testing.T) {
		result, status, errText := latestAnalysisFields(nil)
		assert.Nil(t, result)
		assert.Nil(t, status)
		assert.Nil(t, errText)
	})

	t.Run("a completed run surfaces its result but not an error", func(t *testing.T) {
		r := "the analysis found X"
		run := &domain.AIAnalysisRun{Status: domain.AIAnalysisRunCompleted, Result: &r}
		result, status, errText := latestAnalysisFields(run)
		require.NotNil(t, result)
		assert.Equal(t, "the analysis found X", *result)
		require.NotNil(t, status)
		assert.Equal(t, "completed", *status)
		assert.Nil(t, errText)
	})

	t.Run("a failed run surfaces its error but not a result", func(t *testing.T) {
		e := "provider timed out"
		run := &domain.AIAnalysisRun{Status: domain.AIAnalysisRunFailed, Error: &e}
		result, status, errText := latestAnalysisFields(run)
		assert.Nil(t, result)
		require.NotNil(t, status)
		assert.Equal(t, "failed", *status)
		require.NotNil(t, errText)
		assert.Equal(t, "provider timed out", *errText)
	})

	t.Run("a still-running run surfaces only its status", func(t *testing.T) {
		run := &domain.AIAnalysisRun{Status: domain.AIAnalysisRunRunning}
		result, status, errText := latestAnalysisFields(run)
		assert.Nil(t, result)
		require.NotNil(t, status)
		assert.Equal(t, "running", *status)
		assert.Nil(t, errText)
	})
}

func TestOrEmptySlice(t *testing.T) {
	t.Run("nil becomes an empty, non-nil slice", func(t *testing.T) {
		got := orEmptySlice(nil)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("a non-nil slice, including an already-empty one, passes through unchanged", func(t *testing.T) {
		assert.Equal(t, []string{}, orEmptySlice([]string{}))
		assert.Equal(t, []string{"a", "b"}, orEmptySlice([]string{"a", "b"}))
	})
}
