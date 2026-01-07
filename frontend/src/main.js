/**
 * FRP Manager 2026 - 核心逻辑脚本
 */

import { Events, Browser } from "@wailsio/runtime";
// 后端绑定方法
import { SaveUserConfig, Connect, Disconnect, GetStatus } from "../bindings/mole/moleservice";


// 初始化全局命名空间
window.App = {
    // 1. 内存状态快照
    state: {
        rawConfig: null,    // 后端原始备份
        proxyList: [],      // 当前 UI 代理列表快照
        isRunning: false,   // frp是否运行
        isLoaded: false,    // 是否加载完毕
        isProcessing: false, // 防止按钮连续点击（防抖）
        logs: [], // 内存中的日志数组
        maxLogCount: 200 // 限制最大条数，防止内存溢出
    },

    /**
     * 核心方法：向内存添加日志并更新 UI
     * 支持单条字符串 或 对象数组
     */
    appendLogs(input) {
        // 1. 统一格式：将单条字符串转为数组，确保后续逻辑一致
        const incoming = Array.isArray(input) ? input : [input];

        // 2. 转换成标准的日志对象
        const newEntries = incoming.map(line => ({
            id: Date.now() + Math.random(),
            time: new Date().toLocaleTimeString('zh-CN', { hour12: false }),
            level: this.detectLogLevel(line), // 自动识别 [I]/[E] 等级别
            content: line.trim()
        }));

        // 3. 更新内存（追加并截断）
        this.state.logs = [...this.state.logs, ...newEntries].slice(-this.state.maxLogCount);

        // 4. 触发增量渲染
        this.renderNewLogs(newEntries);
    },

    // 辅助方法：识别日志等级
    detectLogLevel(line) {
        if (line.includes(' [I] ')) return 'success';
        if (line.includes(' [W] ')) return 'warning';
        if (line.includes(' [E] ')) return 'error';
        return 'system'; // 默认级别
    },

    // 切换标签
    showTab(tabId) {
        console.log("切换标签:", tabId); // 用于调试，看控制台是否有输出

        // 1. 移除所有内容的 active 类
        const contents = document.querySelectorAll('.tab-content');
        contents.forEach(c => c.classList.remove('active'));

        // 2. 移除所有导航按钮的 active 类
        const navs = document.querySelectorAll('.nav-item');
        navs.forEach(n => n.classList.remove('active'));

        // 3. 激活当前选中的内容
        const targetContent = document.getElementById(tabId);
        if (targetContent) {
            targetContent.classList.add('active');
        } else {
            console.error("找不到对应的标签内容 ID:", tabId);
        }

        // 4. 激活当前点击的按钮
        // 通过 data-tab 属性查找最准确
        const targetNav = document.querySelector(`.nav-item[data-tab="${tabId}"]`);
        if (targetNav) {
            targetNav.classList.add('active');
        }
    },

    // 初始化监听
    init() {
        console.log("系统启动中...");

        // 监听后端事件
        Events.On('frp-status', (status) => {
            console.log('frp status,', status);
            this.state.isProcessing = false;
            this.refreshStatus();
        });

        Events.On('frp-logs', (event) => {
            console.log('frp logs,', event);
            // 1. 获取后端批量传递的数组
            const logBatch = event.data;
            this.appendLogs(logBatch);

        });

        // 首次加载
        this.refreshStatus();
    },

    // 刷新状态
    async refreshStatus() {
        // 1. 从后端获取当前真实的运行快照
        const status = await GetStatus();
        console.log('后端状态：', status);
        // 2. 将后端真实状态同步到内存 state
        this.state.isRunning = status.isRunning; // 核心：捕获后端已启动的状态
        this.state.rawConfig = JSON.parse(JSON.stringify(status.config));
        this.state.proxyList = (status.config.proxies || []).map(p => ({
            ...p,
            type: p.proxyType || p.type,
            customDomains: Array.isArray(p.domains) ? (p.domains[0] || "") : (p.customDomains || "")
        }));

        // 3. 执行全局渲染
        // renderAll 内部会调用 renderConnectButton
        // 而 renderConnectButton 已经优化为根据 this.state.isRunning 来显示样式
        this.renderAll();

        // 4. 在日志中反馈
        if (this.state.isRunning) {
            this.appendLogs("检测到 FRP 服务已在后台运行");
        } else {
            this.appendLogs("系统就绪，服务待命中");
        }

        this.state.isLoaded = true;
    },

    // 连接/关闭
    async toggleConnect() {
        if (this.state.isProcessing) return;
        this.state.isProcessing = true;
        this.renderConnectButton(); // 立即反馈点击锁定

        try {
            this.state.isRunning ? await Disconnect() : await Connect();
            // 注意：这里不需要手动设置 isRunning = true，
            // 应该等待后端 Events.On 回调触发真正的 render
        } catch (e) {
            this.appendLogs("操作失败: " + e);
            this.state.isProcessing = false;
            this.renderConnectButton();
        }
    },

    // 渲染全部
    renderAll() {
        this.renderConnectButton();
        this.renderConfigFields();
        this.renderProxies();
    },

    // 渲染连接按钮
    renderConnectButton() {
        const btn = document.getElementById('conn-btn');
        const text = document.getElementById('status-text');
        const msg = document.getElementById('status-msg');
        const card = document.querySelector('.hero-status-card');

        // 配置校验
        const isConfigured = this.state.rawConfig?.server?.addr;

        // A. 锁定状态
        if (this.state.isProcessing) {
            btn.disabled = true;
            btn.style.opacity = "0.6";
            btn.querySelector('.btn-text').innerText = "处理中...";
            return;
        }

        // B. 未配置状态
        if (!isConfigured) {
            btn.disabled = true;
            text.innerText = "UnConfig";
            msg.innerHTML = '请先完善 <a href="#" onclick="showTab(\'config\')">配置</a>';
            return;
        }

        // C. 运行状态切换
        btn.disabled = false;
        btn.style.opacity = "1";
        card.classList.toggle('running', this.state.isRunning);
        btn.classList.toggle('btn-danger', this.state.isRunning);

        text.innerText = this.state.isRunning ? "Running" : "Ready";
        text.style.color = this.state.isRunning ? "var(--primary)" : "var(--text-main)";
        btn.querySelector('.btn-text').innerText = this.state.isRunning ? "断开穿透隧道" : "立即建立连接";
        msg.innerText = this.state.isRunning ? "服务正在运行中" : "准备好建立隧道";
    },

    // 渲染服务器配置
    renderConfigFields() {
        const s = this.state.rawConfig?.server || {};
        console.log('服务器配置：', s);
        document.getElementById('server-addr').value = s.addr || "";
        document.getElementById('server-port').value = s.port || 7000;
        document.getElementById('server-token').value = s.token || "";
        document.getElementById('server-remark').value = s.remark || "";
        const auto = document.getElementById('server-autostart');
        if (auto) auto.checked = !!s.autoStart;
    },

    // 渲染添加代理按钮
    renderAddButton() {
        const btn = document.getElementById('add-proxy-btn');
        if (!btn) return;
        const isFull = this.state.proxyList.length >= 3;
        btn.disabled = isFull;
        btn.style.opacity = isFull ? "0.5" : "1";
        btn.innerText = isFull ? "已达数量上限" : "+ 添加规则";
    },

    // 渲染代理
    renderProxies() {
        const container = document.getElementById('proxy-container');
        container.innerHTML = '';

        this.state.proxyList.forEach((p, index) => {
            const isHTTP = p.type === 'http';
            const card = document.createElement('div');
            card.className = `card proxy-card`;
            card.setAttribute('data-type', p.type); // 保留属性，用于 CSS 变色

            card.innerHTML = `
                <div class="proxy-header">
                    <div class="header-left">
                        <select class="p-type-select" onchange="App.updateProxyType(${index}, this.value)">
                            <option value="http" ${p.type === 'http' ? 'selected' : ''}>HTTP</option>
                            <option value="tcp" ${p.type === 'tcp' ? 'selected' : ''}>TCP</option>
                            <option value="udp" ${p.type === 'udp' ? 'selected' : ''}>UDP</option>
                        </select>
                        <span class="proxy-type-tag type-${p.type}">${p.type.toUpperCase()}</span>
                    </div>
                    <button class="btn-delete-text" onclick="App.removeProxy(${index})">
                        <span class="icon">🗑️</span> 删除
                    </button>
                </div>
    
                <div class="form-grid-2">
                    <div class="form-group-mini">
                        <label>规则名称</label>
                        <input type="text" value="${p.name || ''}" oninput="App.state.proxyList[${index}].name = this.value">
                    </div>
                    <div class="form-group-mini">
                        <label>本地端口</label>
                        <input type="number" value="${p.localPort || 80}" oninput="App.state.proxyList[${index}].localPort = parseInt(this.value)||80">
                    </div>
                </div>
    
                <!-- 根据类型切换显示的参数组 -->
                <div class="domain-group" style="display: ${isHTTP ? 'block' : 'none'}; margin-top: 10px;">
                    <label>自定义域名 (Custom Domains)</label>
                    <input type="text" placeholder="e.g. web.example.com" 
                           value="${p.customDomains || ''}" 
                           oninput="App.state.proxyList[${index}].customDomains = this.value">
                </div>
    
                <div class="port-group" style="display: ${!isHTTP ? 'block' : 'none'}; margin-top: 10px;">
                    <label>远程端口 (Remote Port)</label>
                    <input type="number" placeholder="e.g. 8080" 
                           value="${p.remotePort || ''}" 
                           oninput="App.state.proxyList[${index}].remotePort = parseInt(this.value)||8080">
                </div>
    
                <div class="proxy-footer">
                    <div class="status-indicator">
                        <span class="tiny-label">Local IP</span>
                        <input type="text" class="tiny-input" value="${p.localIP || '127.0.0.1'}" 
                               oninput="App.state.proxyList[${index}].localIP = this.value">
                    </div>
                </div>
            `;
            container.appendChild(card);
        });

        this.renderAddButton(); // 更新“添加”按钮状态
    },

    // 添加代理
    addProxy() {
        if (this.state.proxyList.length >= 3) {
            this.appendLogs("最多配置 3 条代理规则");
            return;
        }
        const newProxy = {
            name: "web_" + Math.floor(Math.random() * 1000),
            type: "http",
            localIP: "127.0.0.1",
            localPort: 80,
            domains: ""
        };
        this.state.proxyList.push(newProxy);
        this.renderProxies(); // 重新渲染
    },

    // 删除代理
    removeProxy(index) {
        this.state.proxyList.splice(index, 1);
        this.renderProxies();
        this.appendLogs("规则已移除快照，请点击保存生效");
    },

    // 保存配置
    async saveAllConfig() {
        if (this.state.isProcessing) return;

        // 1. 锁定 UI，显示保存中
        const saveBtn = document.getElementById('save-all-config');
        const statusMsg = document.getElementById('save-status');
        this.state.isProcessing = true;
        if (saveBtn) saveBtn.innerText = "正在保存...";


        // --- A. 数据校验 (Validation) ---
        for (const [index, p] of this.state.proxyList.entries()) {
            const proxyNum = index + 1;
            if (!p.name?.trim()) {
                this.appendLogs(`保存失败：第 ${proxyNum} 条规则缺少名称`);
                statusMsg.innerText = `❌ 保存失败：第 ${proxyNum} 条规则缺少名称`;
                statusMsg.style.color = "var(--danger)";
                return;
            }
            if (!p.localPort || p.localPort <= 0) {
                this.appendLogs(`保存失败：第 ${proxyNum} 条规则本地端口无效`);
                statusMsg.innerText = `❌ 保存失败：第 ${proxyNum} 条规则本地端口无效`;
                statusMsg.style.color = "var(--danger)";
                return;
            }
            if (p.type === 'http' && !p.customDomains?.trim()) {
                this.appendLogs(`保存失败：HTTP 规则 "${p.name}" 必须填写域名`);
                statusMsg.innerText = `❌ 保存失败：HTTP 规则 "${p.name}" 必须填写域名`;
                statusMsg.style.color = "var(--danger)";
                return;
            }
            if (p.type !== 'http' && (!p.remotePort || p.remotePort <= 0)) {
                this.appendLogs(`保存失败：${p.type.toUpperCase()} 规则 "${p.name}" 必须填写远程端口`);
                statusMsg.innerText = `❌ 保存失败：${p.type.toUpperCase()} 规则 "${p.name}" 必须填写远程端口`;
                statusMsg.style.color = "var(--danger)";
                return;
            }
        }

        // --- B. 数据还原 (Mapping) ---
        const proxiesForBackend = this.state.proxyList.map(p => {
            // 提取前端特有字段，保留其他
            const { type, customDomains, ...others } = p;
            const mapped = {
                ...others,
                proxyType: type // 还原字段名
            };

            // 处理域名：将字符串转回后端需要的数组格式
            if (type === 'http') {
                mapped.domains = [customDomains.trim()];
            } else {
                mapped.remotePort = parseInt(p.remotePort);
            }
            return mapped;
        });

        // 2. 收集数据：从 DOM 抓取基础设置，从内存抓取代理列表
        const serverConfig = {
            addr: document.getElementById('server-addr').value,
            port: parseInt(document.getElementById('server-port').value),
            token: document.getElementById('server-token').value,
            autoStart: document.getElementById('server-autostart').checked,
            remark: document.getElementById('server-remark').value
        };

        const finalConfig = {
            server: serverConfig,
            proxies: proxiesForBackend // 直接使用内存中的最新快照
        };

        try {
            // 3. 调用后端 Wails 接口
            const success = await SaveUserConfig(finalConfig);

            if (success) {
                // 4. 更新“原始数据”备份，标记当前内存数据为最新
                this.state.rawConfig = JSON.parse(JSON.stringify(finalConfig));

                if (statusMsg) {
                    statusMsg.innerText = "✅ 配置已保存，需要重新连接";
                    statusMsg.style.color = "var(--primary)";
                }

                this.appendLogs("配置保存成功并已应用到内存");
            }
        } catch (err) {
            if (statusMsg) {
                statusMsg.innerText = "❌ 保存失败";
                statusMsg.style.color = "var(--danger)";
            }
            this.appendLogs("保存失败: " + err);
        } finally {
            // 5. 解除锁定
            this.state.isProcessing = false;
            if (saveBtn) saveBtn.innerText = "保存并应用配置";

            // 6. 重新触发一次全局渲染（确保按钮状态、提示文字同步）
            this.renderAll();
        }
    },

    updateProxyType(index, newType) {
        // 1. 只修改类型，保留其他字段（如 localPort, remotePort 等）
        this.state.proxyList[index].type = newType;

        // 2. 触发重新渲染，UI 会根据新的 type 自动切换显示/隐藏
        this.renderProxies();
    },


    clearLogs() {
        // 1. 核心操作：清空内存中的日志数组
        this.state.logs = [];

        // 2. 更新 DOM：清空日志列表容器
        const list = document.getElementById('log-list');
        if (list) {
            list.innerHTML = '';
        }

        // 3. 记录一条清空日志（使用新的统一入口）
        this.appendLogs("日志缓冲区已成功清空。");

        console.log("Wails 2026: Logs cleared.");
    },

    renderNewLogs(newLogs) {
        const list = document.getElementById('log-list');
        if (!list) return;

        const fragment = document.createDocumentFragment(); // 使用文档片段，减少重绘次数

        newLogs.forEach(log => {
            const item = document.createElement('div');
            item.className = `log-item ${log.level}`;
            item.innerHTML = `
            <div class="log-meta">
                <span class="log-time">${log.time}</span>
                <span class="log-tag">[${log.level.toUpperCase()}]</span>
            </div>
            <div class="log-content">${log.content.trim()}</div>
        `;
            fragment.appendChild(item);
        });

        list.appendChild(fragment);

        // 4. 清理多余的旧 DOM 节点 (保持 DOM 树轻量)
        while (list.children.length > this.state.maxLogCount) {
            list.removeChild(list.firstChild);
        }

        // 5. 滚动到底部
        list.scrollTop = list.scrollHeight;
    },

    /**
     * 复制地址到剪贴板
     */
    copyURL() {
        const url = document.getElementById('subdomain-url').innerText;
        if (!url || url === "fetching...") return;

        navigator.clipboard.writeText(url).then(() => {
            this.appendLogs("地址已复制到剪切板: " + url);
        }).catch(err => {
            this.appendLogs("复制失败: " + err);
        });
    },

    /**
     * 外部链接跳转 (Wails 3 浏览器调用)
     */
    openExternal(url) {
        // 2026 年 Wails 3 建议使用内置的 Browser 模块
        Browser.OpenURL(url).catch(err => {
            console.error("无法打开浏览器:", err);
            this.appendLogs("打开链接失败: " + url);
        });
    },

    /**
     * URL 面板显示逻辑 (基于内存状态驱动)
     */
    checkAndShowURLPanel() {
        const panel = document.getElementById('url-panel');
        if (!panel) return;

        // 优化：直接从内存 state 中查找第一个 HTTP 代理
        const httpProxy = this.state.proxyList.find(p => p.type === 'http' && p.customDomains);

        if (this.state.isRunning && httpProxy) {
            document.getElementById('subdomain-url').innerText = "https://" + httpProxy.customDomains;
            panel.style.display = 'block';
        } else {
            panel.style.display = 'none';
        }
    }

};

// 启动
window.addEventListener('DOMContentLoaded', () => App.init());


