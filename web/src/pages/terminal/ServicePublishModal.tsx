import React, { useEffect, useMemo, useRef, useState } from 'react';
import { AlertCircle, CheckCircle2, Globe2, LoaderCircle, RadioTower, RefreshCw, Trash2, X } from 'lucide-react';
import { api } from '../../api';
import type { BackgroundJob, SystemOverview, VMListeningPort, VMPortForward } from '../../types';

interface ServicePublishModalProps {
  onClose: () => void;
  onReconnect: () => void;
}

const sleep = (ms: number) => new Promise((resolve) => window.setTimeout(resolve, ms));

const sourceLabel = (source: VMListeningPort['source'] | VMPortForward['source']) => {
  if (source === 'manual') return 'Web 终端';
  if (source === 'managed') return 'Docker/系统';
  return '未发布';
};

export const ServicePublishModal: React.FC<ServicePublishModalProps> = ({ onClose, onReconnect }) => {
  const mountedRef = useRef(true);
  const [listeningPorts, setListeningPorts] = useState<VMListeningPort[]>([]);
  const [forwardedPorts, setForwardedPorts] = useState<VMPortForward[]>([]);
  const [overview, setOverview] = useState<SystemOverview | null>(null);
  const [bindAddress, setBindAddress] = useState('127.0.0.1');
  const [vmStatus, setVMStatus] = useState('Stopped');
  const [requiresRestart, setRequiresRestart] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busyPort, setBusyPort] = useState<number | null>(null);
  const [manualPort, setManualPort] = useState('');
  const [serviceName, setServiceName] = useState('');
  const [addToHome, setAddToHome] = useState(true);
  const [message, setMessage] = useState<{ type: 'success' | 'error' | 'warning'; text: string } | null>(null);
  const [jobMessage, setJobMessage] = useState('');

  useEffect(() => () => {
    mountedRef.current = false;
  }, []);

  const loadData = async () => {
    setLoading(true);
    try {
      const [listening, forwards, system] = await Promise.all([
        api.getVMListeningPorts(),
        api.getVMPortForwards(),
        api.getOverview(),
      ]);
      if (!mountedRef.current) return;
      setListeningPorts(listening.ports || []);
      setForwardedPorts(forwards.ports || []);
      setOverview(system);
      setBindAddress(forwards.bindAddress || '127.0.0.1');
      setVMStatus(forwards.vmStatus || listening.vmStatus || system.vm?.status || 'Stopped');
      setRequiresRestart(Boolean(forwards.requiresRestart));
    } catch (err: any) {
      if (!mountedRef.current) return;
      setMessage({ type: 'error', text: `加载端口状态失败：${err.message}` });
    } finally {
      if (mountedRef.current) setLoading(false);
    }
  };

  useEffect(() => {
    void loadData();
  }, []);

  const rows = useMemo(() => {
    const byPort = new Map<number, VMListeningPort>();
    listeningPorts.forEach((port) => byPort.set(port.port, port));
    forwardedPorts.forEach((forwarded) => {
      if (!byPort.has(forwarded.port)) {
        byPort.set(forwarded.port, {
          port: forwarded.port,
          addresses: [],
          process: forwarded.process,
          forwarded: true,
          source: forwarded.source,
          publishable: false,
          reason: forwarded.listening ? undefined : '端口已发布，但 VM 内当前未监听',
        });
      }
    });
    return Array.from(byPort.values()).sort((a, b) => a.port - b.port);
  }, [forwardedPorts, listeningPorts]);

  const waitForJob = async (jobId: string): Promise<BackgroundJob | null> => {
    for (let attempt = 0; attempt < 600; attempt += 1) {
      if (!mountedRef.current) return null;
      const job = await api.getJob(jobId);
      if (!mountedRef.current) return null;
      setJobMessage(job.message || '正在处理…');
      if (job.status !== 'running') return job;
      await sleep(1000);
    }
    throw new Error('后台任务超时，请稍后刷新状态');
  };

  const shortcutURL = (port: number) => {
    const host = bindAddress === '127.0.0.1' || bindAddress === 'localhost'
      ? '127.0.0.1'
      : overview?.system.primaryIP || '127.0.0.1';
    return `http://${host}:${port}`;
  };

  const createShortcut = async (port: number) => {
    if (!addToHome) return;
    await api.createServiceShortcut({
      source: 'manual',
      name: serviceName.trim() || `VM 服务 ${port}`,
      url: shortcutURL(port),
      icon: 'globe',
      description: `Web 终端发布服务 · VM :${port}`,
      enabled: true,
    });
  };

  const handlePublish = async (port: number) => {
    if (!Number.isInteger(port) || port < 1024 || port > 65535) {
      setMessage({ type: 'error', text: '手工发布端口必须在 1024-65535 范围内' });
      return;
    }
    if (!window.confirm('发布端口需要重启 MacBox VM。请确认服务已配置为 Docker restart policy、systemd 或 PM2 等持久运行方式；前台临时进程会在重启后停止。')) return;
    setBusyPort(port);
    setMessage(null);
    setJobMessage('');
    const homeSuffix = addToHome ? '；首页入口已更新' : '';
    try {
      const result = await api.publishVMPort(port);
      if (!mountedRef.current) return;
      if (result.status === 'already_forwarded') {
        try {
          await createShortcut(port);
          setMessage({ type: 'success', text: `端口 ${port} 已经发布，无需重启${homeSuffix}` });
        } catch {
          setMessage({ type: 'warning', text: `端口 ${port} 已成功发布，但首页入口创建失败，可稍后手动添加` });
        }
      } else if (result.jobId) {
        const job = await waitForJob(result.jobId);
        if (!job) return;
        if (job.status !== 'succeeded') throw new Error(job.error || '端口发布失败');
        onReconnect();
        try {
          await createShortcut(port);
          setMessage({ type: job.message?.includes('未监听') ? 'warning' : 'success', text: `${job.message || `端口 ${port} 已发布`}${homeSuffix}` });
        } catch {
          setMessage({ type: 'warning', text: `${job.message || `端口 ${port} 已发布`}，但首页入口创建失败，可稍后手动添加` });
        }
      }
      setManualPort('');
      await loadData();
    } catch (err: any) {
      if (mountedRef.current) {
        setMessage({ type: 'error', text: err.message });
        await loadData();
      }
    } finally {
      if (mountedRef.current) {
        setBusyPort(null);
        setJobMessage('');
      }
    }
  };

  const handleRemove = async (port: number) => {
    if (!window.confirm(`确认取消发布端口 ${port} 吗？这会重启 MacBox VM。`)) return;
    setBusyPort(port);
    setMessage(null);
    setJobMessage('');
    try {
      const result = await api.removeVMPortForward(port);
      if (!mountedRef.current) return;
      if (result.jobId) {
        const job = await waitForJob(result.jobId);
        if (!job) return;
        if (job.status !== 'succeeded') throw new Error(job.error || '取消发布失败');
        onReconnect();
        setMessage({ type: 'success', text: job.message || `端口 ${port} 已取消发布` });
      }
      await loadData();
    } catch (err: any) {
      if (mountedRef.current) {
        setMessage({ type: 'error', text: err.message });
        await loadData();
      }
    } finally {
      if (mountedRef.current) {
        setBusyPort(null);
        setJobMessage('');
      }
    }
  };

  const accessHost = bindAddress === '127.0.0.1' || bindAddress === 'localhost' ? '127.0.0.1' : overview?.system.primaryIP || 'Mac IP';

  return (
    <div className="fixed inset-0 z-[90] flex items-end justify-center bg-slate-950/70 p-0 backdrop-blur-sm sm:items-center sm:p-4" role="dialog" aria-modal="true" aria-labelledby="service-publish-title">
      <div className="flex max-h-[92vh] w-full max-w-3xl flex-col overflow-hidden rounded-t-3xl border border-slate-700 bg-[#101827] shadow-2xl sm:rounded-3xl">
        <div className="flex items-start justify-between gap-3 border-b border-slate-800 px-5 py-4 sm:px-6">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl border border-sky-400/30 bg-sky-400/10 text-sky-300"><RadioTower className="h-5 w-5" /></div>
            <div className="min-w-0">
              <h2 id="service-publish-title" className="text-base font-black text-white">发布 VM 服务</h2>
              <p className="mt-1 text-xs leading-relaxed text-slate-400">将 VM 内监听端口发布到 Mac。局域网访问范围跟随 MacBox 全局网络设置。</p>
            </div>
          </div>
          <button type="button" onClick={onClose} className="rounded-xl p-2 text-slate-400 transition hover:bg-slate-800 hover:text-white" aria-label="关闭"><X className="h-5 w-5" /></button>
        </div>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-5 sm:p-6">
          <div className="rounded-2xl border border-amber-400/30 bg-amber-400/10 p-3 text-xs leading-relaxed text-amber-100">
            发布或取消发布都会重启 MacBox VM。前台临时进程会在重启后停止，请先配置 Docker、systemd 或 PM2 等持久运行方式。
          </div>

          {message && (
            <div className={`flex items-start gap-2 rounded-2xl border p-3 text-xs ${message.type === 'error' ? 'border-rose-400/30 bg-rose-400/10 text-rose-200' : message.type === 'warning' ? 'border-amber-400/30 bg-amber-400/10 text-amber-100' : 'border-emerald-400/30 bg-emerald-400/10 text-emerald-200'}`}>
              {message.type === 'error' ? <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" /> : message.type === 'warning' ? <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" /> : <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />}
              <span>{message.text}</span>
            </div>
          )}

          {jobMessage && <div className="rounded-2xl border border-sky-400/30 bg-sky-400/10 p-3 text-xs text-sky-100">{jobMessage}</div>}

          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h3 className="text-sm font-bold text-white">发现 VM 服务</h3>
              <p className="mt-1 text-[11px] text-slate-500">VM 状态：{vmStatus} · 访问范围：{bindAddress === '127.0.0.1' ? '仅本机' : '局域网'}</p>
            </div>
            <button type="button" onClick={() => void loadData()} disabled={loading || busyPort !== null} className="flex items-center gap-1.5 rounded-xl border border-slate-700 bg-slate-900 px-3 py-2 text-xs font-semibold text-slate-300 transition hover:border-sky-400/50 hover:text-white disabled:opacity-50"><RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />刷新</button>
          </div>

          {loading ? (
            <div className="flex items-center justify-center rounded-2xl border border-slate-800 bg-slate-950/40 py-10 text-xs text-slate-400"><LoaderCircle className="mr-2 h-4 w-4 animate-spin" />正在扫描 VM 监听端口…</div>
          ) : rows.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-slate-700 bg-slate-950/30 p-5 text-center text-xs text-slate-400">{vmStatus === 'Running' ? '当前没有发现 TCP 监听端口，可手动输入端口。' : 'VM 未运行，启动后可扫描监听端口。'}</div>
          ) : (
            <div className="overflow-hidden rounded-2xl border border-slate-800">
              <div className="hidden grid-cols-[5rem_minmax(0,1fr)_7rem_6rem] gap-3 bg-slate-900/80 px-4 py-2 text-[10px] font-bold uppercase tracking-wide text-slate-500 sm:grid"><span>端口</span><span>进程 / 监听地址</span><span>状态</span><span className="text-right">操作</span></div>
              <div className="divide-y divide-slate-800">
                {rows.map((row) => {
                  const forward = forwardedPorts.find((item) => item.port === row.port);
                  const busy = busyPort === row.port;
                  return (
                    <div key={row.port} className="grid gap-2 px-4 py-3 sm:grid-cols-[5rem_minmax(0,1fr)_7rem_6rem] sm:items-center sm:gap-3">
                      <div className="font-mono text-sm font-bold text-sky-300">{row.port}</div>
                      <div className="min-w-0 text-xs text-slate-300"><div className="truncate font-semibold">{row.process || '未知进程'}</div><div className="truncate text-[11px] text-slate-500">{row.addresses.length ? row.addresses.join(', ') : '当前未监听'}</div></div>
                      <div className="text-[11px] text-slate-400">{row.forwarded ? `已发布 · ${sourceLabel(row.source)}` : row.reason || '未发布'}</div>
                      <div className="flex justify-end gap-2">
                        {forward?.removable ? <button type="button" onClick={() => void handleRemove(row.port)} disabled={busy || requiresRestart} className="flex items-center gap-1 rounded-lg border border-rose-400/30 px-2 py-1.5 text-[11px] font-semibold text-rose-300 transition hover:bg-rose-400/10 disabled:opacity-40"><Trash2 className="h-3 w-3" />取消</button> : row.forwarded ? <span className="text-[11px] text-slate-600">由系统管理</span> : <button type="button" onClick={() => void handlePublish(row.port)} disabled={!row.publishable || busy || requiresRestart} className="rounded-lg bg-sky-500 px-2.5 py-1.5 text-[11px] font-bold text-white transition hover:bg-sky-400 disabled:cursor-not-allowed disabled:opacity-35">{busy ? <LoaderCircle className="h-3 w-3 animate-spin" /> : '发布'}</button>}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          <div className="space-y-3 rounded-2xl border border-slate-800 bg-slate-950/40 p-4">
            <div className="flex items-center gap-2 text-sm font-bold text-white"><Globe2 className="h-4 w-4 text-sky-300" />手动输入端口</div>
            <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_8rem]">
              <label className="space-y-1.5"><span className="text-[11px] font-semibold text-slate-400">服务名称（可选）</span><input value={serviceName} onChange={(event) => setServiceName(event.target.value)} placeholder="例如：我的工作台" className="w-full rounded-xl border border-slate-700 bg-slate-900 px-3 py-2.5 text-xs text-white outline-none focus:border-sky-400" /></label>
              <label className="space-y-1.5"><span className="text-[11px] font-semibold text-slate-400">VM 端口</span><input value={manualPort} onChange={(event) => setManualPort(event.target.value.replace(/[^0-9]/g, '').slice(0, 5))} inputMode="numeric" placeholder="3000" className="w-full rounded-xl border border-slate-700 bg-slate-900 px-3 py-2.5 font-mono text-xs text-white outline-none focus:border-sky-400" /></label>
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-slate-400"><span>Mac 访问地址：<code className="text-sky-300">{accessHost}:{manualPort || '端口'}</code>（VM 与 Mac 使用同一端口）</span><button type="button" onClick={() => void handlePublish(Number(manualPort))} disabled={!manualPort || busyPort !== null || requiresRestart} className="rounded-xl bg-sky-500 px-3.5 py-2 text-xs font-bold text-white transition hover:bg-sky-400 disabled:cursor-not-allowed disabled:opacity-40">发布端口</button></div>
            <label className="flex items-start gap-2 text-[11px] text-slate-400"><input type="checkbox" checked={addToHome} onChange={(event) => setAddToHome(event.target.checked)} className="mt-0.5 rounded border-slate-600 bg-slate-900 text-sky-500 focus:ring-sky-400" /><span>发布后添加到首页服务导航</span></label>
            {requiresRestart && <p className="text-[11px] text-amber-200">当前已有 VM 配置等待重启生效，请先完成或取消现有 VM 操作。</p>}
          </div>
        </div>

        <div className="flex justify-end border-t border-slate-800 px-5 py-3 sm:px-6"><button type="button" onClick={onClose} className="rounded-xl border border-slate-700 px-4 py-2 text-xs font-semibold text-slate-300 transition hover:bg-slate-800 hover:text-white">完成</button></div>
      </div>
    </div>
  );
};
