# Proxy & TLS

Every domain drawn through nginx or Caddy to the application it reaches, with its certificate beside it.

<a href="../readme/proxy.png"><img src="../readme/proxy.png" alt="The proxy overview drawing domains through nginx to their applications, over the last hour of traffic across every site"></a>

<p align="center"><sub><b>Proxy overview</b> — domains, applications and the hour's traffic</sub></p>

## What you get

- Every domain drawn through nginx or Caddy to the application it reaches
- Sites written as ordinary config, with a builder for auth and access rules
- Certificates through certbot, including DNS wildcards, with runway at a glance
- A live TLS report, streams, ports and per-site traffic
- Alerts on renewals and certificate drift

<table>
<tr>
<td width="50%" valign="top">
<a href="../readme/proxy-sites.png"><img src="../readme/proxy-sites.png" alt="The sites page with traffic, certificates and applications over every site as one row"></a>
<p align="center"><b>Sites</b><br><sub>Ordinary config, with a builder beside it</sub></p>
</td>
<td width="50%" valign="top">
<a href="../readme/proxy-certificates.png"><img src="../readme/proxy-certificates.png" alt="Certificates one to a row with where each came from, who names it and when it expires, beside the views that issue and renew them"></a>
<p align="center"><b>Certificates</b><br><sub>Every certificate, with its runway</sub></p>
</td>
</tr>
</table>

<a href="../readme/proxy-tls.png"><img src="../readme/proxy-tls.png" alt="A live TLS report: the way the scan reached the name drawn from this server to the root, its findings, the certificate and how the grade was reached"></a>

<p align="center"><sub><b>TLS report</b> — the grade, and exactly how it was reached</sub></p>

---

<p align="center"><a href="network.md">← Network</a> &nbsp;·&nbsp; <a href="../../FEATURES.md"><b>All features</b></a> &nbsp;·&nbsp; <a href="security-backups.md">Security & backups →</a></p>
