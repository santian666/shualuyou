const $ = (selector) => document.querySelector(selector);
const log = $('#log');
const status = $('#status');
let prepared = false;
let running = false;
let dangerous = false;
let modalResolver;
const workflowStateKey = 'ax9000-auto-flash-state-v1';
function loadWorkflow() {
  try { return JSON.parse(localStorage.getItem(workflowStateKey) || '{}'); } catch { return {}; }
}
const savedWorkflow = loadWorkflow();
let selectedModel = savedWorkflow.model === 'AX6000' ? 'AX6000' : 'AX9000';
let ubootWritten = savedWorkflow.ubootWritten === true;
let physicalConfirmed = savedWorkflow.physicalConfirmed === true;
let firmwareUploaded = savedWorkflow.firmwareUploaded === true;
let lastStep = Number(savedWorkflow.lastStep) || (firmwareUploaded ? 6 : physicalConfirmed ? 4 : ubootWritten ? 3 : 0);
let savedBackupDir = savedWorkflow.backupDir || '';
let staticIPApplied = false;
let defaultBackupBaseDir = '';
let firmwareLayout = '';
let adaptersLoaded = false;
let autoRunPassword = '';
let autoRunApproved = false;
let workflowLoaded = false;
let resumeResolver;
let resumeAutoStart = false;
let autoRunModelRequested = false;

async function saveWorkflow(step = lastStep) {
  lastStep = step;
  const state = { model: selectedModel, lastStep, ubootWritten, physicalConfirmed, firmwareUploaded, backupDir: savedBackupDir };
  localStorage.setItem(workflowStateKey, JSON.stringify(state));
  try { await window.go.main.App.SaveWorkflowState(state); } catch (error) { appendLog(`保存刷机进度失败：${String(error)}`); }
}

async function clearWorkflow() {
  ubootWritten = false;
  physicalConfirmed = false;
  firmwareUploaded = false;
  lastStep = 0;
  savedBackupDir = '';
  localStorage.removeItem(workflowStateKey);
  try { await window.go.main.App.ClearWorkflowState(); } catch (error) { appendLog(`清除刷机进度失败：${String(error)}`); }
}

function resetWorkflowDisplay() {
  document.querySelectorAll('#steps li').forEach((item) => item.classList.remove('done', 'active'));
  $('#progress').textContent = '0 / 8';
  $('#start').textContent = '开始自动刷机流程';
  $('#backupDir').value = '';
}

function currentModelConfig() {
  if (selectedModel === 'AX6000') {
    return {
      ubootURL: 'http://192.168.31.1',
      physical: '请严格按顺序操作：\n\n1. 拔掉红米 AX6000 电源。\n2. 用牙签按住 Reset 键，不要松手。\n3. 保持按住 Reset 的同时插入电源。\n4. 继续按住至少 15 秒后再松开。\n5. 此 U-Boot 不亮指示灯，请观察电脑网口是否闪烁。\n6. 保持电脑网线连接 LAN 2、3 或 4 口。\n\n全部完成后点击“我已完成，继续检测”。'
    };
  }
  return {
    ubootURL: 'http://192.168.1.1',
    physical: '请严格按顺序操作：\n\n1. 关闭路由器电源开关。\n2. 拔掉电源，等待至少 10 秒。\n3. 用牙签按住 Reset 键，不要松手。\n4. 插入电源并打开开关，继续按住 Reset。\n5. 看到黄灯或橙灯长亮后松开 Reset。\n6. 保持电脑网线连接 LAN 2、3 或 4 口。\n\n全部完成后点击“我已完成，继续检测”。'
  };
}

function updateModelUI() {
  const ax6000 = selectedModel === 'AX6000';
  $('#model').value = selectedModel;
  $('#modelSubtitle').textContent = ax6000 ? '红米 AX6000 / RB06，Factory=mtd4、FIP=mtd5 强校验' : '小米 AX9000 / RA70，MIBIB 与双 APPSBL 强校验';
  $('#developerFirmwareLabel').textContent = ax6000 ? '官方 1.2.8 固件（解锁失败时回退）' : 'AX9000 开发版固件';
  $('#developerFirmwareRow').hidden = ax6000;
  $('#mibibRow').hidden = ax6000;
  $('#ubootLabel').textContent = ax6000 ? 'AX6000 FIP 引导文件' : 'U-Boot 文件';
  $('#firmwareLabel').textContent = ax6000 ? 'AX6000 OpenWrt 固件' : 'OpenWrt factory 固件';
  $('#materialsStepTitle').textContent = ax6000 ? '同目录材料与固件布局校验' : '同目录材料与固件格式校验';
  $('#unlockBackupTitle').textContent = ax6000 ? '自动解锁 SSH 并备份全部 MTD' : '自动解锁 SSH 并备份全部 MTD';
  $('#unlockBackupHint').textContent = ax6000 ? '自动调用 XMiR；SSH 可用后只读备份全部 11 个物理 MTD 到 AX6000\\backups\\时间目录。' : '自动调用 XMiR；必要时回退开发版，SSH 可用后只读备份全部物理 MTD 到 AX9000\\backups\\时间目录。';
  $('#deviceStepTitle').textContent = ax6000 ? 'RB06、分区与备份核对' : 'RA70、分区与备份核对';
  $('#partitionHint').textContent = ax6000 ? '确认 RB06、mtd4=Factory、mtd5=FIP，并检查当前设备的 Factory/FIP 备份均为 2 MiB。' : '确认 RA70、mtd1=MIBIB、mtd15=APPSBL_1、mtd16=APPSBL，并检查三份关键备份均为 1 MiB。';
  $('#networkHint').textContent = ax6000 ? 'Windows 10/11 自动设为 192.168.31.2，打开 192.168.31.1；确认上传完成后恢复 DHCP。' : 'Windows 10/11 自动设为 192.168.1.10，打开 192.168.1.1；确认上传完成后恢复 DHCP。';
  $('#materialsStepHint').textContent = ax6000 ? '自动读取 EXE 同级 AX6000；校验固定 FIP 大小/MD5/SHA256，并解析固件 BOARD、kernel、root 与 MTD layout。' : '自动读取 EXE 同级 AX9000；强校验开发固件、MIBIB、U-Boot、factory.ubi 的文件类型和工具包 SHA256。';
  $('#bootWriteTitle').textContent = ax6000 ? '写入并回读校验 FIP' : '写入并回读校验三处分区';
  $('#bootWriteHint').textContent = ax6000 ? '备份完成后才上传 FIP；核对远端 SHA256，写入 mtd5，再按实际文件长度回读 SHA256。' : '备份完成后才写入：MIBIB→mtd1，U-Boot→mtd15/mtd16；每处分区按实际文件长度回读 SHA256。';
  $('#physicalStepHint').textContent = ax6000 ? '断电，按住 Reset 插电并保持至少 15 秒；此 U-Boot 不亮指示灯。' : '断电 10 秒，按住 Reset 通电，黄/橙灯长亮后确认。';
}

async function loadModelPaths() {
  const paths = await window.go.main.App.GetDefaultPaths(selectedModel);
  $('#unlockTool').value = paths.unlockTool;
  $('#developerFirmware').value = paths.developerFirmware;
  $('#mibib').value = paths.mibib;
  $('#uboot').value = paths.uboot;
  $('#firmware').value = paths.firmware;
  $('#backupDir').value = savedBackupDir || '';
  $('#backupDir').placeholder = `自动备份到 ${paths.backupBaseDir}`;
  defaultBackupBaseDir = paths.backupBaseDir;
  appendLog(`已加载 ${paths.model} 工具目录：${paths.toolsDir}`);
  appendLog(`备份根目录已准备：${paths.backupBaseDir}`);
  ['unlockTool', 'developerFirmware', 'mibib', 'uboot', 'firmware', 'backupDir'].forEach((id) => { $('#' + id).title = $('#' + id).value || $('#' + id).placeholder; });
}

function showResumeChoice() {
  const descriptions = ['准备阶段', 'SSH 已解锁', '原厂备份已完成', 'U-Boot 已写入并校验', '等待重新进入 U-Boot', '已打开 U-Boot 上传页', '已确认手动上传完成'];
  $('#resume-message').textContent = `上次流程停在：${descriptions[lastStep] || `步骤 ${lastStep}`}。\n\n更新时间和详细结果请查看执行日志。密码不会被保存，若重新开始需要再次输入。`;
  $('#materials-modal').hidden = true;
  $('#resume-modal').hidden = false;
  return new Promise((resolve) => { resumeResolver = resolve; });
}

function closeResumeChoice(continuePrevious) {
  $('#resume-modal').hidden = true;
  if (resumeResolver) resumeResolver(continuePrevious);
  resumeResolver = null;
}

function appendLog(message) {
  const time = new Date().toLocaleTimeString('zh-CN', { hour12: false });
  if (log.textContent === '等待检查材料…') log.textContent = '';
  log.textContent += `[${time}] ${message}\n`;
  log.scrollTop = log.scrollHeight;
}

function toast(message, error = false) {
  const box = $('#toast');
  box.textContent = message;
  box.className = error ? 'show error' : 'show';
  setTimeout(() => { box.className = ''; }, 3200);
}

function setRunning(value, text = '') {
  running = value;
  $('#start').disabled = value || !prepared;
  $('#inspect').disabled = value;
  $('#backupRouter').disabled = value;
  $('#resetProgress').disabled = value;
  $('#model').disabled = value;
  $('#cancel').disabled = !value || dangerous;
  status.textContent = text || (value ? '执行中' : '等待操作');
  status.className = value ? 'status running' : 'status';
}

function setDangerous(value) {
  dangerous = value;
  $('#cancel').disabled = !running || value;
}

function markStep(index, state) {
  document.querySelectorAll('#steps li').forEach((item, i) => {
    item.classList.toggle('active', i === index && state === 'active');
    if (i === index && state === 'done') item.classList.add('done');
  });
  $('#progress').textContent = `${document.querySelectorAll('#steps li.done').length} / 8`;
}

function ask(title, message, danger = false, okText = '确认，继续', openURL = '') {
  $('#modalTitle').textContent = title;
  $('#modalMessage').textContent = message;
  $('#modalOK').textContent = okText;
  $('#dangerCheckWrap').hidden = !danger;
  $('#dangerCheck').checked = false;
  $('#modalOK').disabled = danger;
  $('#modalOpenURL').hidden = !openURL;
  $('#modalOpenURL').dataset.url = openURL;
  $('#modal').hidden = false;
  requestAnimationFrame(() => (danger ? $('#dangerCheck') : $('#modalOK')).focus());
  return new Promise((resolve) => { modalResolver = resolve; });
}

function closeModal(value) {
  $('#modal').hidden = true;
  if (modalResolver) modalResolver(value);
  modalResolver = null;
}

function sshConfig() {
  return { host: $('#sshHost').value.trim(), username: $('#sshUser').value.trim(), password: $('#sshPassword').value };
}

async function inspectMaterials() {
  try {
    const result = await window.go.main.App.InspectMaterials(selectedModel, $('#developerFirmware').value, $('#mibib').value, $('#uboot').value, $('#firmware').value, $('#backupDir').value);
    prepared = true;
    if (!ubootWritten) firmwareUploaded = false;
    $('#start').disabled = false;
    markStep(0, 'done');
    if (result.developerFirmware.name) appendLog(`解锁固件：${result.developerFirmware.name}，SHA256 ${result.developerFirmware.sha256}`);
    if (result.mibib.name) appendLog(`MIBIB：${result.mibib.name}，SHA256 ${result.mibib.sha256}`);
    appendLog(`${selectedModel === 'AX6000' ? 'FIP' : 'U-Boot'}：${result.uboot.name}，SHA256 ${result.uboot.sha256}`);
    appendLog(`OpenWrt：${result.firmware.name}，${(result.firmware.size / 1024 / 1024).toFixed(1)} MiB`);
    firmwareLayout = result.firmwareLayout || '';
    if (result.firmwareBoard) appendLog(`AX6000 固件 BOARD：${result.firmwareBoard}；U-Boot 必须选择 MTD layout：${firmwareLayout}`);
    if (result.backupFiles.length) {
      appendLog(`已识别 ${result.backupFiles.length} 个关键原厂备份文件`);
      toast('通用材料和备份检查通过');
    } else {
      appendLog('尚未选择原厂备份；SSH 解锁后程序会自动备份当前路由器');
      toast('通用刷机材料检查通过');
    }
    $('#materials-modal').hidden = true;
  } catch (error) {
    prepared = false;
    $('#start').disabled = true;
    toast(String(error), true);
    appendLog(`材料检查失败：${String(error)}`);
    $('#materials-modal').hidden = false;
  }
}

function tryAutoRun() {
  if (!autoRunPassword || !prepared || !adaptersLoaded || !workflowLoaded || running) return;
  $('#routerPassword').value = autoRunPassword;
  autoRunPassword = '';
  autoRunApproved = true;
  $('#materials-modal').hidden = true;
  appendLog('已接受本次受控自动启动授权，开始执行原有安全流程');
  runWorkflow();
}

async function runWorkflow() {
  if (running || !prepared) return;
  if (!ubootWritten && !$('#routerPassword').value) {
    toast('请先输入小米后台密码', true);
    return;
  }
  if (!ubootWritten) {
    if (autoRunApproved) {
      autoRunApproved = false;
    } else if (!(await ask('确认开始自动刷机', '软件将自动处理 SSH 解锁、原厂备份和引导分区写入。进入 U-Boot 后，软件会设置静态 IP 并打开浏览器，由你在 FIRMWARE UPDATE 页面手动上传固件。\n\n危险写入期间不能停止程序或断电。', true, '确认并开始'))) return;
  }
  setRunning(true, '自动执行中');
  try {
    if (!ubootWritten) {
      markStep(1, 'active');
      status.textContent = '自动解锁 SSH';
      setDangerous(true);
      const router = await window.go.main.App.AutoUnlockSSH(selectedModel, $('#unlockTool').value, $('#developerFirmware').value, $('#sshHost').value.trim(), $('#routerPassword').value);
      setDangerous(false);
      appendLog(`SSH 自动解锁完成：${router.model}`);
      await saveWorkflow(1);
      status.textContent = '自动完整备份';
      const backup = await window.go.main.App.BackupRouter(sshConfig(), defaultBackupBaseDir);
      $('#backupDir').value = backup.directory;
      savedBackupDir = backup.directory;
      await saveWorkflow(2);
      appendLog(`本机备份目录：${backup.directory}`);
      await inspectMaterials();
      markStep(1, 'done');

      markStep(2, 'active');
      appendLog(`确认型号：${router.model}`);
      appendLog(selectedModel === 'AX6000' ? `分区排列：mtd4=${router.mtd.mtd4}，mtd5=${router.mtd.mtd5}` : `分区排列：mtd15=${router.mtd.mtd15}，mtd16=${router.mtd.mtd16}`);
      markStep(2, 'done');

      markStep(3, 'active');
      status.textContent = '写入并校验 U-Boot';
      setDangerous(true);
      await window.go.main.App.FlashUBoot({ model: selectedModel, ssh: sshConfig(), mibibPath: $('#mibib').value, ubootPath: $('#uboot').value });
      setDangerous(false);
      ubootWritten = true;
      await saveWorkflow(3);
      $('#start').textContent = '继续自动刷机流程';
      markStep(3, 'done');
    }

    const adapter = $('#adapter').value;
    if (!adapter) throw new Error('未选择有线网卡');

    if (!firmwareUploaded) {
      if (!physicalConfirmed) {
        markStep(4, 'active');
        status.textContent = '等待物理操作';
        if (!(await ask('需要你操作路由器（唯一一次物理操作）', currentModelConfig().physical, false, '我已完成，继续检测'))) throw new Error('用户取消');
        physicalConfirmed = true;
        await saveWorkflow(4);
        markStep(4, 'done');
      }

      markStep(5, 'active');
      status.textContent = '自动设置网卡并检测 U-Boot';
      await window.go.main.App.SetStaticIP(adapter, selectedModel);
      staticIPApplied = true;
      appendLog(`已为 ${selectedModel} 自动设置 U-Boot 静态 IP（兼容 Windows 10/11）`);
      await window.go.main.App.OpenURL(currentModelConfig().ubootURL);
      appendLog(`已请求系统默认浏览器打开：${currentModelConfig().ubootURL}`);
      const ubootPage = await window.go.main.App.WaitForUBoot(currentModelConfig().ubootURL);
      appendLog(ubootPage.message);
      await saveWorkflow(5);
      markStep(5, 'done');

      markStep(6, 'active');
      status.textContent = '等待浏览器手动上传固件';
      appendLog(`已打开 FIRMWARE UPDATE：${currentModelConfig().ubootURL}；软件不会自动上传或点击 Update`);
      const layoutInstruction = selectedModel === 'AX6000' ? `\n\n重要：网页中的“Choose mtd layout”必须选择：${firmwareLayout}` : '';
      const uploadDone = await ask('请在浏览器手动上传固件', `软件已打开 ${currentModelConfig().ubootURL}${layoutInstruction}\n\n然后手动选择这个固件：\n${$('#firmware').value}\n\n等待上传到 100%，再按页面提示点击 Update。确认页面写入完成、路由器已正常启动后，再回到软件点击“上传完成”。`, false, '上传完成', currentModelConfig().ubootURL);
      if (!uploadDone) throw new Error('用户暂停手动上传固件');
      firmwareUploaded = true;
      await saveWorkflow(6);
      markStep(6, 'done');
    } else {
      status.textContent = '恢复网络并等待 OpenWrt';
      appendLog('已记录浏览器手动上传完成，跳过 U-Boot 检测和重复上传');
    }

    markStep(7, 'active');
    status.textContent = '等待 OpenWrt 启动';
    await window.go.main.App.SetDHCP(adapter);
    staticIPApplied = false;
    appendLog(`浏览器手动上传已完成，已恢复有线网卡 DHCP，开始检测 ${selectedModel === 'AX6000' ? '10.0.0.1' : '192.168.1.1'}`);
    const openwrt = await window.go.main.App.WaitForOpenWrt(selectedModel);
    appendLog(openwrt.message);
    markStep(7, 'done');
    await clearWorkflow();
    status.textContent = '刷机完成';
    status.className = 'status success';
    toast('刷机流程完成');
  } catch (error) {
    setDangerous(false);
    const message = String(error);
    if (message.includes('等待 U-Boot')) {
      physicalConfirmed = false;
      await saveWorkflow(3);
    }
    status.textContent = message.includes('用户取消') ? '已暂停' : '执行失败';
    status.className = 'status error';
    appendLog(message.includes('用户取消') ? '流程已由用户暂停' : `执行失败：${message}`);
    if (!message.includes('用户取消')) toast(message, true);
  } finally {
    setDangerous(false);
    if (staticIPApplied) {
      try {
        await window.go.main.App.SetDHCP($('#adapter').value);
        appendLog('流程异常结束，已自动恢复网卡 DHCP');
      } catch (restoreError) {
        appendLog(`自动恢复 DHCP 失败：${String(restoreError)}`);
      }
      staticIPApplied = false;
    }
    running = false;
    $('#inspect').disabled = false;
    $('#backupRouter').disabled = false;
    $('#resetProgress').disabled = false;
    $('#model').disabled = false;
    $('#cancel').disabled = true;
    $('#start').disabled = !prepared;
  }
}

document.querySelectorAll('[data-pick]').forEach((button) => button.addEventListener('click', async () => {
  try {
    const path = await window.go.main.App.SelectFile('选择文件', button.dataset.types.split(';'));
    if (path) $("#" + button.dataset.pick).value = path;
  } catch (error) { toast(String(error), true); }
}));
$('#pickBackup').addEventListener('click', async () => {
  try { const path = await window.go.main.App.SelectDirectory('选择当前路由器的原厂备份目录'); if (path) $('#backupDir').value = path; } catch (error) { toast(String(error), true); }
});
$('#inspect').addEventListener('click', inspectMaterials);
$('#model').addEventListener('change', async (event) => {
  const nextModel = event.target.value === 'AX6000' ? 'AX6000' : 'AX9000';
  if (nextModel === selectedModel) return;
  if (lastStep > 0 || ubootWritten || physicalConfirmed || firmwareUploaded) {
    if (!(await ask('切换路由器型号', `切换到 ${nextModel} 会清除当前机型的软件断点记录，但不会删除备份文件。`, false, '清零并切换'))) {
      event.target.value = selectedModel;
      return;
    }
  }
  await clearWorkflow();
  resetWorkflowDisplay();
  selectedModel = nextModel;
  prepared = false;
  updateModelUI();
  await loadModelPaths();
  await inspectMaterials();
});
$('#backupRouter').addEventListener('click', async () => {
  if (running) return;
  setRunning(true, '正在备份');
  try {
    const result = await window.go.main.App.BackupRouter(sshConfig(), defaultBackupBaseDir);
    $('#backupDir').value = result.directory;
    appendLog(`全部 ${result.files.length} 个分区已保存：${result.directory}`);
    appendLog(`SHA256 清单：${result.manifestPath}`);
    toast('当前路由器完整备份成功');
    if ($('#uboot').value && $('#firmware').value) await inspectMaterials();
  } catch (error) {
    appendLog(`备份失败：${String(error)}`);
    toast(String(error), true);
  } finally {
    running = false;
    $('#inspect').disabled = false;
    $('#backupRouter').disabled = false;
    $('#resetProgress').disabled = false;
    $('#model').disabled = false;
    $('#cancel').disabled = true;
    $('#start').disabled = !prepared;
    status.textContent = '等待操作';
    status.className = 'status';
  }
});
$('#start').addEventListener('click', runWorkflow);
$('#cancel').addEventListener('click', () => window.go.main.App.Cancel());
$('#resetProgress').addEventListener('click', async () => {
  if (running) return;
  if (!(await ask('清零刷机进度', '确定清除软件记录的断点位置并回到初始状态吗？\n\n此操作不会删除已经下载到电脑里的路由器备份文件。路由器已经手动刷好时，可以安全清零。', false, '确认清零'))) return;
  await clearWorkflow();
  resetWorkflowDisplay();
  await inspectMaterials();
  appendLog('已手动清零刷机进度；电脑中的路由器备份文件未删除');
  toast('刷机进度已清零');
});
$('#clearLog').addEventListener('click', () => { log.textContent = '等待检查材料…'; });
window.addEventListener('keydown', async (event) => {
  if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === 'p' && !running) {
    event.preventDefault();
    $('#routerPassword').focus();
  } else if (event.ctrlKey && event.key === 'Enter' && !running && $('#modal').hidden) {
    event.preventDefault();
    if (!prepared) await inspectMaterials();
    runWorkflow();
  }
});
$('#modalCancel').addEventListener('click', () => closeModal(false));
$('#modalOK').addEventListener('click', () => closeModal(true));
$('#modalOpenURL').addEventListener('click', async (event) => {
  try {
    await window.go.main.App.OpenURL(event.currentTarget.dataset.url);
    toast('已重新打开上传页');
  } catch (error) {
    toast(String(error), true);
  }
});
$('#dangerCheck').addEventListener('change', (event) => { $('#modalOK').disabled = !event.target.checked; });
$('#resumeContinue').addEventListener('click', () => closeResumeChoice(true));
$('#resumeRestart').addEventListener('click', () => closeResumeChoice(false));
$('#openTutorial').addEventListener('click', async () => {
  const url = selectedModel === 'AX6000' ? 'https://cloud.tencent.com/developer/article/2276735' : 'https://www.getpicion.com/archives/1595';
  try { await window.go.main.App.OpenURL(url); } catch (error) { toast(String(error), true); }
});
$('#openFirmwareSelector').addEventListener('click', async () => {
  const url = selectedModel === 'AX6000' ? 'https://firmware-selector.openwrt.org/?target=mediatek%2Ffilogic&id=xiaomi_redmi-router-ax6000' : 'https://firmware-selector.immortalwrt.org/?target=qualcommax%2Fipq807x&id=xiaomi_ax9000-stock';
  try { await window.go.main.App.OpenURL(url); } catch (error) { toast(String(error), true); }
});
$('#materialsReady').addEventListener('click', () => { $('#materials-modal').hidden = true; });
window.runtime.EventsOn('task-log', appendLog);

const workflowStateReady = Promise.all([window.go.main.App.LoadWorkflowState(), window.go.main.App.TakeAutoRunModel()]).then(async ([state, autorunModel]) => {
  const serverHasProgress = state.lastStep > 0 || state.ubootWritten || state.physicalConfirmed || state.firmwareUploaded;
  if (serverHasProgress) {
    selectedModel = state.model === 'AX6000' ? 'AX6000' : 'AX9000';
    lastStep = Number(state.lastStep) || 0;
    ubootWritten = state.ubootWritten === true;
    physicalConfirmed = state.physicalConfirmed === true;
    firmwareUploaded = state.firmwareUploaded === true;
    savedBackupDir = state.backupDir || '';
  }
  if (autorunModel === 'AX6000' || autorunModel === 'AX9000') {
    if (serverHasProgress && selectedModel !== autorunModel) throw new Error(`断点属于 ${selectedModel}，拒绝按 ${autorunModel} 自动继续`);
    selectedModel = autorunModel;
    autoRunModelRequested = true;
    localStorage.setItem(workflowStateKey, JSON.stringify({ model: selectedModel, lastStep, ubootWritten, physicalConfirmed, firmwareUploaded, backupDir: savedBackupDir }));
    appendLog(`已按自动启动参数选择 ${selectedModel}`);
  } else if (serverHasProgress) {
    localStorage.setItem(workflowStateKey, JSON.stringify({ model: selectedModel, lastStep, ubootWritten, physicalConfirmed, firmwareUploaded, backupDir: savedBackupDir }));
  } else if (lastStep > 0 || ubootWritten || physicalConfirmed || firmwareUploaded) {
    await saveWorkflow(lastStep);
  }
  workflowLoaded = true;
}).catch((error) => {
  workflowLoaded = true;
  appendLog(`读取上次刷机进度失败，将按未完成状态安全启动：${String(error)}`);
});

workflowStateReady.then(async () => {
  updateModelUI();
  await loadModelPaths();
  if (lastStep > 0 || ubootWritten || physicalConfirmed || firmwareUploaded) {
    const continuePrevious = autoRunModelRequested ? true : await showResumeChoice();
    if (!continuePrevious) {
      await clearWorkflow();
      resetWorkflowDisplay();
      appendLog('用户选择重新开始刷机，已清除上次流程进度');
    } else {
      resumeAutoStart = true;
      appendLog(`${autoRunModelRequested ? '自动' : '用户选择'}继续上次刷机，将从步骤 ${lastStep + 1} 继续`);
    }
  }
  if (savedBackupDir) $('#backupDir').value = savedBackupDir;
  if (ubootWritten) {
    $('#start').textContent = '继续自动刷机流程';
    appendLog('检测到上次流程已完成 U-Boot 写入，将从物理操作节点继续，避免重复写入');
    [1, 2, 3].forEach((index) => markStep(index, 'done'));
  }
  if (physicalConfirmed) {
    markStep(4, 'done');
  }
  if (firmwareUploaded) {
    markStep(5, 'done');
    markStep(6, 'done');
  }
  await inspectMaterials();
  if (resumeAutoStart) {
    resumeAutoStart = false;
    if (ubootWritten) {
      tryAutoRun();
    } else {
      appendLog('上次流程尚未完成 U-Boot 写入，请重新输入小米后台密码后点击“开始自动刷机流程”');
    }
  }
  tryAutoRun();
}).catch((error) => appendLog(`自动加载同目录工具失败：${String(error)}`));

window.go.main.App.GetAdapters().then((items) => {
  $('#adapter').innerHTML = '<option value="">请选择有线网卡</option>' + items.map((item) => `<option value="${item.name}">${item.name}${item.ipv4.length ? '（' + item.ipv4.join(', ') + '）' : ''}</option>`).join('');
  const preferred = items.find((item) => item.ipv4.some((ip) => ip.startsWith('192.168.31.')))
    || items.find((item) => /以太网|ethernet/i.test(item.name) && /up/i.test(item.flags));
  if (preferred) {
    $('#adapter').value = preferred.name;
    appendLog(`已自动选择连接路由器的网卡：${preferred.name}`);
  }
  adaptersLoaded = true;
  tryAutoRun();
}).catch((error) => { $('#adapter').innerHTML = '<option value="">读取网卡失败</option>'; appendLog(String(error)); });
window.go.main.App.GetLogPath().then((path) => { $('#logPath').textContent = path || '日志文件创建失败'; });
window.go.main.App.TakeAutoRunPassword().then((password) => {
  autoRunPassword = password || '';
  tryAutoRun();
});
