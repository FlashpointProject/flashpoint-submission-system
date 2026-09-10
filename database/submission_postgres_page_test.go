package database

import (
	"reflect"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func TestPGPlatformDatePageEligibility(t *testing.T) {
	filter := &types.SubmissionsFilter{PlatformPartial: utils.StrPtr("Flash,!Unity")}
	q, err := newPGSubmissionSearch(filter, 1)
	require.NoError(t, err)
	require.True(t, pgPlatformDatePageEligible(filter, q))
	for _, platform := range []string{"Unity", "HTML5", "!Flash", "%", "never-matches"} {
		copy := *filter
		copy.PlatformPartial = &platform
		require.True(t, pgPlatformDatePageEligible(&copy, q), "eligibility must not depend on platform contents")
	}
	for _, order := range []string{"created_at", "updated_at"} {
		copy := *q
		copy.order, copy.offset = order, 100
		require.True(t, pgPlatformDatePageEligible(filter, &copy))
		copy.offset = pgPlatformDatePageMaxPrefix - copy.limit
		require.True(t, pgPlatformDatePageEligible(filter, &copy))
		copy.offset++
		require.False(t, pgPlatformDatePageEligible(filter, &copy))
	}
	for _, change := range []func(*pgSubmissionSearch){
		func(q *pgSubmissionSearch) { q.order = "newest_file_size" },
		func(q *pgSubmissionSearch) { q.offset = -1 },
		func(q *pgSubmissionSearch) { q.limit = 0 },
		func(q *pgSubmissionSearch) { q.limit = pgPlatformDatePageMaxPrefix + 1 },
		func(q *pgSubmissionSearch) { q.filters = nil },
	} {
		copy := *q
		change(&copy)
		require.False(t, pgPlatformDatePageEligible(filter, &copy))
	}
	require.False(t, pgPlatformDatePageEligible(nil, q))
	require.False(t, pgPlatformDatePageEligible(&types.SubmissionsFilter{}, q))

	allowed := map[string]bool{"PlatformPartial": true, "ResultsPerPage": true, "Page": true, "OrderBy": true, "AscDesc": true, "ExcludeLegacy": true}
	// Every additional field must fail closed, including future filter fields.
	typ := reflect.TypeOf(types.SubmissionsFilter{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if allowed[field.Name] {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			copy := *filter
			v := reflect.ValueOf(&copy).Elem().Field(i)
			switch v.Kind() {
			case reflect.Ptr:
				v.Set(reflect.New(v.Type().Elem()))
			case reflect.Slice:
				v.Set(reflect.MakeSlice(v.Type(), 1, 1))
			case reflect.Bool:
				v.SetBool(true)
			default:
				t.Fatalf("add representative nonzero value for %s", field.Name)
			}
			require.False(t, pgPlatformDatePageEligible(&copy, q))
		})
	}
}
