module.exports = {
  apps: [{
    name: "pcd",
    script: "./pcd",
    // Point this at wherever the pcd binary + .env live (e.g. ~/.powercodedeck).
    cwd: process.env.HOME + "/.powercodedeck",
    env_file: ".env",
  }]
}
