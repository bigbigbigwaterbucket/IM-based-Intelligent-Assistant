import { contextBridge } from "electron";

contextBridge.exposeInMainWorld("agentPilotDesktop", {
  platform: "electron",
});
