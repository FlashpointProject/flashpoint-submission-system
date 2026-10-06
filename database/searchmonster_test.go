package database

import (
	"reflect"
	"testing"
)

func TestAddMultifilter(t *testing.T) {
	cases := []struct {
		name, input            string
		filters, masterFilters []string
		args                   []interface{}
	}{
		{name: "empty", input: " , ,  "},
		{name: "include OR", input: "Flash, HTML5", filters: []string{"((meta.platform LIKE ?) OR (meta.platform LIKE ?))"}, masterFilters: []string{"((platform LIKE ?) OR (platform LIKE ?))"}, args: []interface{}{"%Flash%", "%HTML5%"}},
		{name: "exclude AND", input: "!Flash, !HTML5", filters: []string{"((meta.platform NOT LIKE ?) AND (meta.platform NOT LIKE ?))"}, masterFilters: []string{"((platform NOT LIKE ?) AND (platform NOT LIKE ?))"}, args: []interface{}{"%Flash%", "%HTML5%"}},
		{name: "includes precede exclusions regardless of input order", input: " !excluded, included, , other, !last ", filters: []string{"((meta.platform LIKE ?) OR (meta.platform LIKE ?))", "((meta.platform NOT LIKE ?) AND (meta.platform NOT LIKE ?))"}, masterFilters: []string{"((platform LIKE ?) OR (platform LIKE ?))", "((platform NOT LIKE ?) AND (platform NOT LIKE ?))"}, args: []interface{}{"%included%", "%other%", "%excluded%", "%last%"}},
		// LIKE wildcards are currently supported; quotes must stay bound data.
		{name: "wildcards and SQL punctuation remain bound", input: "a_%,' OR 1=1 --", filters: []string{"((meta.platform LIKE ?) OR (meta.platform LIKE ?))"}, masterFilters: []string{"((platform LIKE ?) OR (platform LIKE ?))"}, args: []interface{}{"%a_%%", "%' OR 1=1 --%"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, withMaster := range []bool{false, true} {
				var master *string
				if withMaster {
					name := "platform"
					master = &name
				}
				filters, masterFilters, args, masterArgs := addMultifilter("meta.platform", master, tc.input,
					[]string{"existing = ?"}, []string{"legacy = ?"}, []interface{}{int64(7)}, []interface{}{int64(8)})
				wantFilters := append([]string{"existing = ?"}, tc.filters...)
				wantArgs := append([]interface{}{int64(7)}, tc.args...)
				wantMasterFilters := []string{"legacy = ?"}
				wantMasterArgs := []interface{}{int64(8)}
				if withMaster {
					wantMasterFilters = append(wantMasterFilters, tc.masterFilters...)
					wantMasterArgs = append(wantMasterArgs, tc.args...)
				}
				if !reflect.DeepEqual(filters, wantFilters) || !reflect.DeepEqual(args, wantArgs) {
					t.Fatalf("master=%v: filters=%v args=%v; want %v %v", withMaster, filters, args, wantFilters, wantArgs)
				}
				if !reflect.DeepEqual(masterFilters, wantMasterFilters) || !reflect.DeepEqual(masterArgs, wantMasterArgs) {
					t.Fatalf("master=%v: legacy filters=%v args=%v; want %v %v", withMaster, masterFilters, masterArgs, wantMasterFilters, wantMasterArgs)
				}
			}
		})
	}
}
