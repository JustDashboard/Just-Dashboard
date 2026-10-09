# Shipped native profile inode acceptance

This fixture checkpoint extends the existing accepted owner lifecycles with a canonical unchanged
intent. It is based on the isolated shipped helper-v10 fix, not the private ordered controller
adapter. Native acceptance is pending until the source-matched runs below are recorded.

The automatic fixture covers direct networkd, Debian NetworkManager keyfiles and explicit-policy
Netplan/networkd. The static fixture covers Ubuntu Netplan/NetworkManager exact-origin recovery.
Debian Netplan/NetworkManager automatic RUN-profile admission remains a separate open boundary.
Each selected owner keeps its existing acquisition, DNS/routes, process-death, confirmation and
cleanup checks. Real systemd timer dispatch and reboot remain outside these namespace fixtures.

After an ordinary intent has been saved and confirmed canonically, both fixtures apply that same
intent twice. Every selected authored/generated file must have literally equal captured prior and
candidate bytes, distinct captured inodes, the exact candidate inode at the selected path and the
exact displaced authored inode in staging. A new helper exposing exactly `jd-native-manager-v10`
must restore the original authored inode during rollback, retain full configured/runtime/boot
agreement, and remove owned stages/checkpoints. The second unchanged transaction is confirmed;
a terminal fresh-helper retry must retain the exact candidate inode and the durable decision.

Existing v8 evidence keeps its original source/helper attribution. This document does not claim
structural, cold, or timer acceptance.
