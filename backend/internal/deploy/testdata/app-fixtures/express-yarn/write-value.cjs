// The value a build variable carries is written at build time, the way a
// bundler inlines one, so serving it proves the build step received it.
require('fs').writeFileSync('value.txt', process.env.PUBLIC_API_URL ?? '')
