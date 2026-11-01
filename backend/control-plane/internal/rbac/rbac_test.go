package rbac

import "testing"

func TestAuthorizeDefaultDeny(t *testing.T) {
	res := Resource{}
	d := Authorize(nil, PermNodesRead, res)
	if d.Allowed {
		t.Fatal("bağlama yokken erişim verilmemeli (default-deny)")
	}
	if d.Reason != ReasonNoBindings {
		t.Fatalf("neden: %s", d.Reason)
	}
}

func TestAuthorizeAllScope(t *testing.T) {
	bindings := []Binding{{RoleName: "admin", ScopeType: ScopeAll, Permissions: AllPermissions}}
	for _, perm := range AllPermissions {
		if d := Authorize(bindings, perm, Resource{NodeID: "n1"}); !d.Allowed {
			t.Fatalf("admin tüm izinlere sahip olmalı: %s (%s)", perm, d.Reason)
		}
	}
}

func TestAuthorizeNodeScope(t *testing.T) {
	bindings := []Binding{{
		RoleName: "op", ScopeType: ScopeNode, ScopeID: "node-a",
		Permissions: []string{PermNodesWrite},
	}}
	if d := Authorize(bindings, PermNodesWrite, Resource{NodeID: "node-a"}); !d.Allowed {
		t.Fatalf("node scope eşleşmeli: %s", d.Reason)
	}
	if d := Authorize(bindings, PermNodesWrite, Resource{NodeID: "node-b"}); d.Allowed {
		t.Fatal("başka node reddedilmeli")
	}
	if d := Authorize(bindings, PermNodesWrite, Resource{}); d.Allowed {
		t.Fatal("global kaynak node-scope ile açılamaz")
	}
	if d := Authorize(bindings, PermUsersWrite, Resource{NodeID: "node-a"}); d.Allowed {
		t.Fatal("verilmeyen izin reddedilmeli")
	}
}

func TestAuthorizeGroupScope(t *testing.T) {
	bindings := []Binding{{
		RoleName: "op", ScopeType: ScopeGroup, ScopeID: "grp-1",
		Permissions: []string{PermTasksWrite},
	}}
	inGroup := Resource{NodeID: "n1", NodeGroupIDs: []string{"grp-1", "grp-2"}}
	otherGroup := Resource{NodeID: "n1", NodeGroupIDs: []string{"grp-9"}}
	global := Resource{}

	if d := Authorize(bindings, PermTasksWrite, inGroup); !d.Allowed {
		t.Fatalf("grup üyesi node izinli olmalı: %s", d.Reason)
	}
	if d := Authorize(bindings, PermTasksWrite, otherGroup); d.Allowed {
		t.Fatal("başka grup reddedilmeli")
	}
	if d := Authorize(bindings, PermTasksWrite, global); d.Allowed {
		t.Fatal("grup scope global kaynağı kapsamaz")
	}
	// Grup bilinmiyorsa (çözümlenemedi) reddet.
	unknown := Resource{NodeID: "n1"}
	if d := Authorize(bindings, PermTasksWrite, unknown); d.Allowed {
		t.Fatal("grup listesi boşken reddedilmeli")
	}
}

func TestAuthorizePermissionMiss(t *testing.T) {
	bindings := []Binding{{RoleName: "viewer", ScopeType: ScopeAll, Permissions: []string{PermNodesRead}}}
	d := Authorize(bindings, PermNodesWrite, Resource{})
	if d.Allowed || d.Reason != ReasonPermissionMiss {
		t.Fatalf("permission miss bekleniyordu: %+v", d)
	}
}

func TestValidatePermission(t *testing.T) {
	for _, p := range AllPermissions {
		if err := ValidatePermission(p); err != nil {
			t.Fatalf("katalog izni reddedildi: %s", p)
		}
	}
	// Enjeksiyon/typo koruması: katalog dışı isimler kabul edilmez.
	for _, bad := range []string{"*", "nodes:*", "nodes:read OR 1=1", "nodes:drop", ""} {
		if err := ValidatePermission(bad); err == nil {
			t.Fatalf("geçersiz izin kabul edildi: %q", bad)
		}
	}
}
