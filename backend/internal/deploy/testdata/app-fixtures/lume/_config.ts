import lume from "lume/mod.ts";

const site = lume({ dest: "./public" });
site.data("api", Deno.env.get("PUBLIC_API_URL") ?? "none");

export default site;
