#!/bin/sh
set -eu
stand=$(mktemp -d "${TMPDIR:-/tmp}/hint95-fixtures.XXXXXX")
cd "$stand"
mkdir -p primary/nested second-fixture detached identity primary/existing
git init -q primary
git init -q primary/nested
cat > primary/alpha.go <<'GO'
package alpha

func Shared() string { return "primary" }
func LookupBeacon(s string) string { return "beacon:" + s }
type Catalog struct{}
GO
cat > primary/methods.go <<'GO'
package alpha

func (Catalog) Open() string { return "open" }
func (Catalog) Close() string { return "close" }
func Mutable(s string) string { return "old:" + s }
GO
cat > second-fixture/beta.go <<'GO'
package beta

func Shared() string { return "second" }
func SecondaryOnly() string { return "beta" }
GO
printf 'package nested\nfunc HiddenFromParent() {}\n' > primary/nested/hidden.go
printf 'package detached\nfunc FormerMember() {}\n' > detached/former.go
printf 'package alpha\nfunc Broken(\n' > primary/malformed.go
printf 'Unsupported by the declaration extractor.\n' > primary/omitted.txt
printf 'label=before\n' > primary/settings.txt
git -C primary add alpha.go methods.go settings.txt
git -C primary -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm fixture
printf '%s\n' "$stand"
