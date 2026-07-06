module.exports = {
  apps: [{
    name: "pcd",
    script: "./pcd",
    // Point this at wherever the pcd binary + .env live (e.g. ~/PowerCodeDeck).
    cwd: process.env.HOME + "/PowerCodeDeck",
    env_file: ".env",
  }]
}
