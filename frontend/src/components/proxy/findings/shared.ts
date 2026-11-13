/**
 * The ports the security catalogue treats as a database or a control plane —
 * the same judgement the stream form applies before forwarding one.
 */
export const DANGEROUS_PORTS: Record<number, string> = {
  5432: "PostgreSQL",
  3306: "MySQL",
  6379: "Redis",
  27017: "MongoDB",
  11211: "memcached",
  9200: "Elasticsearch",
  2375: "the Docker API",
}
