# Proxy & TLS

Every domain drawn through nginx or Caddy to the application it reaches, with its certificate beside it.

<a href="../readme/proxy.png"><img src="../readme/proxy.png" alt="The proxy overview drawing domains through nginx to their applications"></a>

<p align="center"><sub><b>Proxy overview</b> — domains, applications and certificates</sub></p>

## What you get

- Every domain drawn through nginx or Caddy to the application it reaches
- Sites written as ordinary config, with a builder for auth and access rules
- Certificates through certbot, including DNS wildcards, with runway at a glance
- A live TLS report, streams, ports and per-site traffic
- Alerts on renewals and certificate drift

<table>
<tr>
<td width="50%" valign="top">
<a href="../readme/proxy-sites.png"><img src="../readme/proxy-sites.png" alt="The sites page with certificates, applications and every site as a card"></a>
<p align="center"><b>Sites</b><br><sub>Ordinary config, with a builder beside it</sub></p>
</td>
<td width="50%" valign="top">
<a href="../readme/proxy-certificates.png"><img src="../readme/proxy-certificates.png" alt="Certificates with their expiry and automatic renewal"></a>
<p align="center"><b>Certificates</b><br><sub>Every certificate, with its runway</sub></p>
</td>
</tr>
</table>

<a href="../readme/proxy-tls.png"><img src="../readme/proxy-tls.png" alt="A live TLS report with its grade, certificate, protocol and how the grade was reached"></a>

<p align="center"><sub><b>TLS report</b> — the grade, and exactly how it was reached</sub></p>

---

<p align="center"><a href="network.md">← Network</a> &nbsp;·&nbsp; <a href="../../FEATURES.md"><b>All features</b></a> &nbsp;·&nbsp; <a href="security-backups.md">Security & backups →</a></p>
