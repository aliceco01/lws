**Title:** Test revision reuse on rollback in Hash mode

#### What type of PR is this?
/kind cleanup

#### What this PR does / why we need it
#1341 fixed the duplicate ControllerRevision created when an LWS rolls back to its previous template during an update. Its integration test covers the default Ordinal group identity only. `reconcileHash` creates the revision of an updated LWS from its own call site, so this adds the same rollback check for `groupIdentity: Hash`: after A→B→A, the leader Deployment returns to A's revision key and the LWS still has two revisions.

`ExpectRevisions` also checks the leader StatefulSet, which Hash mode doesn't have, so the test counts the revisions directly.

#### Which issue(s) this PR fixes
Follow-up to #1341 (#1338).

#### Special notes for your reviewer
The test passes on main. With only the `reconcileHash` call site switched back to `revisionutils.CreateRevision`, it fails with 3 revisions where 2 are expected, so it guards the Hash path specifically.

#### Does this PR introduce a user-facing change?
```release-note
NONE
```
