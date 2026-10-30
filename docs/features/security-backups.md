# Security & backups

A verdict on how exposed the host is, and a map of everything that is not backed up yet.

<a href="../readme/security.png"><img src="../readme/security.png" alt="The security overview drawing the ways in, from the internet through the firewall to the shell"></a>

<p align="center"><sub><b>Security</b> — exposure, SSH, logins and who is attacking</sub></p>

## Security

- A verdict on the host: exposure, SSH, logins and who is attacking
- fail2ban, CrowdSec and Suricata where they run

<table>
<tr>
<td width="50%" valign="top">
<a href="../readme/security-intrusion.png"><img src="../readme/security-intrusion.png" alt="Addresses blocked across engines with fail2ban's jails and readings"></a>
<p align="center"><b>Intrusion</b><br><sub>What is blocked, by which engine, and why</sub></p>
</td>
<td width="50%" valign="top">
<a href="../readme/security-ssh.png"><img src="../readme/security-ssh.png" alt="The SSH daemon drawn with its ways in and its login readings"></a>
<p align="center"><b>SSH</b><br><sub>Who knocks, how they get in, and what is allowed</sub></p>
</td>
</tr>
</table>

## Backups

- Scheduled backups to disk, S3, Backblaze B2, an SFTP server or Google Drive, OneDrive, Dropbox, WebDAV and the other services rclone speaks, encrypted on the way out, and native database dumps
- A coverage map of everything that is *not* backed up yet
- Single-file and in-place restore, on this server or a fresh one: import what a destination holds and restore it there, its Docker volumes made again

<a href="../readme/backups.png"><img src="../readme/backups.png" alt="Backups drawn as what is covered flowing to each destination, with every job below"></a>

<p align="center"><sub><b>Backups</b> — what is covered, and what is not</sub></p>

The dashboard's own security model is in [SECURITY.md](../../SECURITY.md).

---

<p align="center"><a href="proxy-tls.md">← Proxy & TLS</a> &nbsp;·&nbsp; <a href="../../FEATURES.md"><b>All features</b></a> &nbsp;·&nbsp; <a href="server-accounts.md">Server & accounts →</a></p>
