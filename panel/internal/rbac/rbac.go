// Package rbac is the single source of truth for permission checks. The API
// middleware is the only enforcement point; handlers never roll their own.
package rbac

// Permission names must match the catalog seeded in migration 00001_auth.sql.
type Permission string

const (
	Wildcard Permission = "*"

	UserRead    Permission = "user.read"
	UserCreate  Permission = "user.create"
	UserDisable Permission = "user.disable"

	NodeRead   Permission = "node.read"
	NodeCreate Permission = "node.create"
	NodeDelete Permission = "node.delete"

	AuditRead Permission = "audit.read"
	RoleRead  Permission = "role.read"

	ServerCreate Permission = "server.create"
	ServerDelete Permission = "server.delete"
	ServerUpdate Permission = "server.update"
	ServerPower  Permission = "server.power"

	ConsoleRead  Permission = "console.read"
	ConsoleWrite Permission = "console.write"
	FileRead     Permission = "file.read"
	FileWrite    Permission = "file.write"

	PluginInstall Permission = "plugin.install"
	BackupCreate  Permission = "backup.create"
	BackupRestore Permission = "backup.restore"

	ScheduleManage Permission = "schedule.manage"
	SubuserManage  Permission = "subuser.manage"
)

// All returns every known permission (the catalog).
func All() []string {
	return []string{
		string(Wildcard),
		string(UserRead), string(UserCreate), string(UserDisable),
		string(NodeRead), string(NodeCreate), string(NodeDelete),
		string(AuditRead), string(RoleRead),
		string(ServerCreate), string(ServerDelete), string(ServerUpdate), string(ServerPower),
		string(ConsoleRead), string(ConsoleWrite), string(FileRead), string(FileWrite),
		string(PluginInstall), string(BackupCreate), string(BackupRestore),
		string(ScheduleManage), string(SubuserManage),
	}
}

// Valid reports whether p is a known permission (or the wildcard).
func Valid(p string) bool {
	for _, known := range All() {
		if known == p {
			return true
		}
	}
	return false
}

// Has reports whether the granted permission set satisfies want. A wildcard
// grant satisfies everything.
func Has(granted []string, want Permission) bool {
	for _, g := range granted {
		if g == string(want) || g == string(Wildcard) {
			return true
		}
	}
	return false
}
