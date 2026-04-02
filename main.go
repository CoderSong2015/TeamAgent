package main

import (
	"chat_server/agent"
	"chat_server/team"
	"chat_server/tools"
	"chat_server/web"
	"log"
	"net/http"
	"os"
)

const addr = ":8088"

func main() {
	if ws := os.Getenv("WORKSPACE"); ws != "" {
		tools.SetWorkspace(ws)
	}
	tools.RegisterSubAgentTools()
	log.Printf("工作目录: %s", tools.WorkspacePath)
	log.Printf("已注册工具: %d 个", len(tools.GetToolDefs()))
	log.Printf("文件写入: %v | 命令执行: %v", tools.WriteEnabled, tools.CommandEnabled)

	agent.Init()
	team.Init()

	http.HandleFunc("/", web.ServeIndex)
	http.HandleFunc("/api/agents", agent.HandleAgents)
	http.HandleFunc("/api/agent/", agent.HandleAgent)
	http.HandleFunc("/api/teams", team.HandleTeams)
	http.HandleFunc("/api/team/", team.HandleTeam)
	http.HandleFunc("/api/docs", web.HandleDocs)
	http.HandleFunc("/api/doc/", web.HandleDoc)

	log.Printf("服务启动 → http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
