package service

import "github.com/argusops/argusops/internal/domain"

// latestAnalysisFields derives the LatestAnalysis/LatestAnalysisStatus/
// LatestAnalysisError trio domain.Alert and domain.Incident both expose
// from a single AIAnalysisRunRepository.LatestRun result (nil if no
// analysis has ever been requested) -- shared by AlertService.Get and
// IncidentService.Get so the two can't drift on what each status means.
func latestAnalysisFields(run *domain.AIAnalysisRun) (result, status, errText *string) {
	if run == nil {
		return nil, nil, nil
	}
	s := string(run.Status)
	status = &s
	if run.Status == domain.AIAnalysisRunCompleted {
		result = run.Result
	}
	if run.Status == domain.AIAnalysisRunFailed {
		errText = run.Error
	}
	return result, status, errText
}

// tagsVisible implements the design handoff's tag-based access rule:
// "Visible records = records whose tags intersect the current user's
// allowedTags (or all records if allowedTags is empty)". Shared by
// AlertService and IncidentService for both list filtering (pushed into
// SQL, see ListAlertsFilter.AllowedTags) and single-record checks in Get/
// mutating methods, where a record already fetched by RLS-scoped id still
// needs this second, finer-grained check.
func tagsVisible(allowedTags, recordTags []string) bool {
	if len(allowedTags) == 0 {
		return true
	}
	allowed := make(map[string]struct{}, len(allowedTags))
	for _, t := range allowedTags {
		allowed[t] = struct{}{}
	}
	for _, t := range recordTags {
		if _, ok := allowed[t]; ok {
			return true
		}
	}
	return false
}

// orEmptySlice normalizes a nil slice to an empty one. Every text[] column
// in this schema is `not null default '{}'` (tags, keywords, allowed_tools,
// ...), but a Go nil slice encodes as SQL NULL rather than '{}' once it's
// passed as an explicit query argument -- the column default only applies
// when no value is given at all. JSON request bodies that omit an array
// field, or send `null` for it, decode to a nil Go slice, so every service
// method that writes one of these columns from caller-supplied input must
// run it through this first. Found via scripts/smoke-test.sh failing on a
// real NOT NULL violation, not by inspection -- see git history.
func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
