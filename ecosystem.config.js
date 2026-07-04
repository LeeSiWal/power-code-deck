module.exports = {
  apps: [{
    name: "pcd",
    script: "./pcd",
    // Point this at wherever the pcd binary + .env live (e.g. ~/.agentdeck).
    cwd: "/Users/siwal/code/agentdeck-go",
    env_file: ".env",
  }]
}
