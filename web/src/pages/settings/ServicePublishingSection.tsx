import React, { useEffect, useState } from 'react';
import { AlertCircle, CheckCircle2, LoaderCircle, RadioTower, RefreshCw, Trash2 } from 'lucide-react';
import { api } from '../../api';
import type { BackgroundJob, SystemOverview, VMPortForward } from '../../types';

const sleep = (ms: number) => new Promise((resolve) => window.setTimeout(resolve, ms));

export const ServicePublishingSection: React.FC = () => {
  const [ports, setPorts] = useState<VMPortForward[]>([]);
  const [overview, setOverview] = useState<SystemOverview | null>(null);
  const [bindAddress, setBindAddress] = useState('127.0.0.1');
  const [vmStatus, setVMStatus] = useState('Stopped');
  const [requiresRestart, setRequiresRestart] = useState(false);
  const [portInput, setPortInput] = useState('');
  const [loading, setLoading] = useState(true);
  const [busyPort, setBusyPort] = useState<number | null>(null);
  const [message, setMessage] = useState<{ type: 'success' | 'error' | 'warning'; text: string } | null>(null);

  const loadData = async () => {
    setLoading(true);
    try {
      const [forwards, system] = await Promise.all([api.getVMPortForwards(), api.getOverview()]);
      setPorts(forwards.ports || []);
      setOverview(system);
      setBindAddress(forwards.bindAddress || '127.0.0.1');
      setVMStatus(forwards.vmStatus || system.vm?.status || 'Stopped');
      setRequiresRestart(Boolean(forwards.requiresRestart));
    } catch (err: any) {
      setMessage({ type: 'error', text: `加载服务发布状态失败：${err.message}` });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void loadData(); }, []);

  const waitForJob = async (jobId: string): Promise<BackgroundJob> => {
    for (let attempt = 0; attempt < 600; attempt += 1) {
      const job = await api.getJob(jobId);
      if (job.status !== 'running') return job;
      await sleep(1000);
    }
    throw new Error('后台任务超时，请稍后刷新状态');
  };

  const runPublish = async (port: number) => {
    if (!Number.isInteger(port) || port < 1024 || port > 65535) {
      setMessage({ type: 'error', text: '手工发布端口必须在 1024-65535 范围内' });
      return;
    }
    if (!window.confirm('发布端口需要重启 MacBox VM。请确认服务已配置为 Docker、systemd 或 PM2 等持久运行方式。')) return;
    setBusyPort(port);
    setMessage(null);
    try {
      const result = await api.publishVMPort(port);
      if (result.status === 'already_forwarded') {
        setMessage({ type: 'success', text: `端口 ${port} 已经发布，无需重复操作` });
      } else if (result.jobId) {
        const job = await waitForJob(result.jobId);
        if (job.status !== 'succeeded') throw new Error(job.error || '端口发布失败');
        setMessage({ type: job.message?.includes('未监听') ? 'warning' : 'success', text: job.message || `端口 ${port} 已发布` });
      }
      setPortInput('');
      await loadData();
    } catch (err: any) {
      setMessage({ type: 'error', text: err.message });
      await loadData();
    } finally {
      setBusyPort(null);
    }
  };

  const handleRemove = async (port: number) => {
    if (!window.confirm(`确认取消发布端口 ${port} 吗？这会重启 MacBox VM。`)) return;
    setBusyPort(port);
    setMessage(null);
    try {
      const result = await api.removeVMPortForward(port);
      if (result.jobId) {
        const job = await waitForJob(result.jobId);
        if (job.status !== 'succeeded') throw new Error(job.error || '取消发布失败');
        setMessage({ type: 'success', text: job.message || `端口 ${port} 已取消发布` });
      }
      await loadData();
    } catch (err: any) {
      setMessage({ type: 'error', text: err.message });
      await loadData();
    } finally {
      setBusyPort(null);
    }
  };

  const scope = bindAddress === '127.0.0.1' ? '仅本机' : '局域网';
  const primaryIP = overview?.system.primaryIP || 'Mac IP';

  return (
    <div className="space-y-5 rounded-3xl border border-slate-800/80 bg-slate-900/70 p-5 shadow-xl sm:p-6">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-3.5">
          <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl border border-sky-500/30 bg-sky-500/15 text-sky-300"><RadioTower className="h-6 w-6" /></div>
          <div><h3 className="text-lg font-bold text-white">服务发布</h3><p className="mt-1 text-xs leading-relaxed text-slate-400">管理 Web Terminal 手工发布的 VM TCP 端口。端口范围跟随 MacBox 全局网络设置：{scope}（{bindAddress}）。</p></div>
        </div>
        <button type="button" onClick={() => void loadData()} disabled={loading || busyPort !== null} className="flex h-9 items-center gap-1.5 rounded-xl border border-slate-700 bg-slate-800 px-3 text-xs font-semibold text-slate-200 transition hover:bg-slate-700 disabled:opacity-50"><RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />刷新</button>
      </div>

      {message && <div className={`flex items-start gap-2 rounded-2xl border p-3 text-xs ${message.type === 'error' ? 'border-rose-400/30 bg-rose-400/10 text-rose-200' : message.type === 'warning' ? 'border-amber-400/30 bg-amber-400/10 text-amber-100' : 'border-emerald-400/30 bg-emerald-400/10 text-emerald-200'}`}>{message.type === 'error' || message.type === 'warning' ? <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" /> : <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />}<span>{message.text}</span></div>}

      <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-slate-400"><span>VM 状态：<strong className="text-slate-200">{vmStatus}</strong></span><span>访问范围：<strong className="text-sky-300">{scope}</strong></span>{requiresRestart && <span className="text-amber-300">等待 VM 重启生效</span>}</div>

      {loading ? <div className="flex items-center justify-center rounded-2xl border border-slate-800 bg-slate-950/40 py-10 text-xs text-slate-400"><LoaderCircle className="mr-2 h-4 w-4 animate-spin" />正在加载端口状态…</div> : ports.length === 0 ? <div className="rounded-2xl border border-dashed border-slate-700 bg-slate-950/30 p-5 text-center text-xs text-slate-400">当前没有已发布端口。</div> : <div className="overflow-hidden rounded-2xl border border-slate-800"><div className="hidden grid-cols-[5rem_7rem_1fr_7rem] gap-3 bg-slate-950/50 px-4 py-2 text-[10px] font-bold uppercase tracking-wide text-slate-500 sm:grid"><span>端口</span><span>来源</span><span>状态 / 地址</span><span className="text-right">操作</span></div><div className="divide-y divide-slate-800">{ports.map((port) => { const busy = busyPort === port.port; return <div key={port.port} className="grid gap-2 px-4 py-3 sm:grid-cols-[5rem_7rem_1fr_7rem] sm:items-center sm:gap-3"><span className="font-mono text-sm font-bold text-sky-300">{port.port}</span><span className="text-xs text-slate-300">{port.source === 'manual' ? 'Web 终端' : 'Docker/系统'}</span><span className={`text-xs ${port.listening ? 'text-emerald-300' : 'text-amber-300'}`}>{port.listening ? `正常监听 · ${bindAddress === '127.0.0.1' ? '127.0.0.1' : primaryIP}:${port.port}` : '已发布，VM 内当前未监听'}</span><span className="flex justify-end">{port.removable ? <button type="button" onClick={() => void handleRemove(port.port)} disabled={busy || requiresRestart} className="flex items-center gap-1 rounded-lg border border-rose-400/30 px-2 py-1.5 text-[11px] font-semibold text-rose-300 transition hover:bg-rose-400/10 disabled:opacity-40"><Trash2 className="h-3 w-3" />取消</button> : <span className="text-[11px] text-slate-600">由系统管理</span>}</span></div>; })}</div></div>}

      <div className="rounded-2xl border border-slate-800 bg-slate-950/40 p-4">
        <div className="mb-3 text-sm font-bold text-white">手动发布新端口</div>
        <div className="flex flex-col gap-3 sm:flex-row"><input value={portInput} onChange={(event) => setPortInput(event.target.value.replace(/[^0-9]/g, '').slice(0, 5))} inputMode="numeric" placeholder="例如 3000" className="min-w-0 flex-1 rounded-xl border border-slate-700 bg-slate-900 px-3 py-2.5 font-mono text-xs text-white outline-none focus:border-sky-400" /><button type="button" onClick={() => void runPublish(Number(portInput))} disabled={!portInput || busyPort !== null || requiresRestart} className="rounded-xl bg-sky-500 px-4 py-2.5 text-xs font-bold text-white transition hover:bg-sky-400 disabled:cursor-not-allowed disabled:opacity-40">发布端口</button></div>
        <p className="mt-2 text-[11px] leading-relaxed text-slate-500">V1 固定 VM 端口与 Mac 端口相同，不支持编辑 host port；只允许发布 1024-65535。</p>
      </div>
    </div>
  );
};
