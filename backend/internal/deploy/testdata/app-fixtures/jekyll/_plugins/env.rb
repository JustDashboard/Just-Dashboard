Jekyll::Hooks.register :site, :after_init do |site|
  site.config["api_url"] = ENV.fetch("PUBLIC_API_URL", "none")
end
