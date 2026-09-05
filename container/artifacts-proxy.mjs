import net from "node:net";

const socketPath = process.argv[2];
if (!socketPath) {
  console.error("artifact MCP proxy requires a Unix socket path");
  process.exit(2);
}

const socket = net.createConnection(socketPath);
process.stdin.pipe(socket);
socket.pipe(process.stdout);

socket.on("error", (error) => {
  console.error(`artifact MCP proxy: ${error.message}`);
  process.exitCode = 1;
});

socket.on("close", () => {
  process.exit();
});
