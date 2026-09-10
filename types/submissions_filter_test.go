package types

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func filterString(field, value string) SubmissionsFilter {
	var f SubmissionsFilter
	reflect.ValueOf(&f).Elem().FieldByName(field).Set(reflect.ValueOf(&value))
	return f
}

func TestSubmissionsFilterValidateStringChoices(t *testing.T) {
	cases := []struct {
		field  string
		values []string
	}{
		{"AssignedStatusTesting", []string{"assigned", "unassigned"}},
		{"AssignedStatusVerification", []string{"assigned", "unassigned"}},
		{"RequestedChangedStatus", []string{"none", "ongoing"}},
		{"ApprovalsStatus", []string{"none", "approved"}},
		{"VerificationStatus", []string{"none", "verified"}},
		{"AssignedStatusTestingMe", []string{"assigned", "unassigned"}},
		{"AssignedStatusVerificationMe", []string{"assigned", "unassigned"}},
		{"RequestedChangedStatusMe", []string{"none", "ongoing"}},
		{"ApprovalsStatusMe", []string{"no", "yes"}},
		{"VerificationStatusMe", []string{"no", "yes"}},
		{"AssignedStatusTestingUser", []string{"assigned", "unassigned"}},
		{"AssignedStatusVerificationUser", []string{"assigned", "unassigned"}},
		{"RequestedChangedStatusUser", []string{"none", "ongoing"}},
		{"ApprovalsStatusUser", []string{"no", "yes"}},
		{"VerificationStatusUser", []string{"no", "yes"}},
		{"LastUploaderNotMe", []string{"yes"}},
		{"OrderBy", []string{"uploaded", "updated", "size"}},
		{"AscDesc", []string{"asc", "desc"}},
		{"SubscribedMe", []string{"no", "yes"}},
		{"IsExtreme", []string{"no", "yes", "No", "Yes"}},
		{"IsContentChange", []string{"no", "yes"}},
		{"IsFrozen", []string{"no", "yes"}},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			for _, value := range append(append([]string{}, tc.values...), "", "invalid", strings.ToUpper(tc.values[0])) {
				t.Run(value, func(t *testing.T) {
					f := filterString(tc.field, value)
					if strings.HasSuffix(tc.field, "User") && value != "" {
						id := int64(12)
						f.AssignedStatusUserID = &id
					}
					valid := value == ""
					for _, accepted := range tc.values {
						valid = valid || value == accepted
					}
					if err := f.Validate(); (err == nil) != valid {
						t.Fatalf("Validate() = %v, want valid=%v", err, valid)
					}
					if value == "" && !reflect.ValueOf(f).FieldByName(tc.field).IsNil() {
						t.Fatal("empty option should normalize to nil")
					}
				})
			}
		})
	}
}

func TestSubmissionsFilterValidateNumericBounds(t *testing.T) {
	for _, field := range []string{"SubmitterID", "ResultsPerPage", "Page", "AssignedStatusUserID"} {
		t.Run(field, func(t *testing.T) {
			for _, value := range []int64{-1, 0, 1, 1000} {
				var f SubmissionsFilter
				reflect.ValueOf(&f).Elem().FieldByName(field).Set(reflect.ValueOf(&value))
				if field == "AssignedStatusUserID" && value > 0 {
					status := "yes"
					f.ApprovalsStatusUser = &status
				}
				if err := f.Validate(); (err == nil) != (value >= 0) {
					t.Fatalf("value %d: Validate() = %v", value, err)
				}
				if value == 0 && !reflect.ValueOf(f).FieldByName(field).IsNil() {
					t.Fatal("zero should normalize to nil")
				}
			}
		})
	}
	for _, ids := range [][]int64{nil, {}, {1, 2}, {1, 0}, {-1}} {
		f := SubmissionsFilter{SubmissionIDs: ids}
		valid := true
		for _, id := range ids {
			valid = valid && id > 0
		}
		if err := f.Validate(); (err == nil) != valid {
			t.Fatalf("IDs %v: Validate() = %v", ids, err)
		}
	}
}

func TestSubmissionsFilterValidateLeavesDefaultsUnset(t *testing.T) {
	// SQL search owns pagination/order defaults; validation must not impose them.
	f := SubmissionsFilter{}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f, SubmissionsFilter{}) {
		t.Fatalf("unexpected defaults: %#v", f)
	}
}

func TestSubmissionsFilterValidateUserFiltersRequirePositiveID(t *testing.T) {
	for field, values := range map[string][]string{
		"AssignedStatusTestingUser":      {"assigned", "unassigned"},
		"AssignedStatusVerificationUser": {"assigned", "unassigned"},
		"RequestedChangedStatusUser":     {"ongoing", "none"},
		"ApprovalsStatusUser":            {"yes", "no"},
		"VerificationStatusUser":         {"yes", "no"},
	} {
		for _, value := range values {
			for _, id := range []*int64{nil, new(int64), filterInt64(-1), filterInt64(1), filterInt64(12)} {
				name := "missing"
				if id != nil {
					name = fmt.Sprint(*id)
				}
				t.Run(field+"/"+value+"/"+name, func(t *testing.T) {
					f := filterString(field, value)
					f.AssignedStatusUserID = id
					err := f.Validate()
					if wantValid := id != nil && *id > 0; (err == nil) != wantValid {
						t.Fatalf("Validate() = %v, want valid=%v", err, wantValid)
					}
				})
			}
		}
	}
}

func filterInt64(value int64) *int64 { return &value }

func TestSubmissionsFilterValidateUserIDAloneAccepted(t *testing.T) {
	id := int64(12)
	f := SubmissionsFilter{AssignedStatusUserID: &id}
	if err := f.Validate(); err != nil {
		t.Fatalf("an unused positive user ID should be accepted: %v", err)
	}
	// Keeping the ID allows a caller to reuse its selection without activating
	// any user-specific predicates; SQL only reads it when such a filter is set.
	if !reflect.DeepEqual(f, SubmissionsFilter{AssignedStatusUserID: &id}) {
		t.Fatalf("unexpected filter mutation: %#v", f)
	}
}
