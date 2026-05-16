import React, { useCallback, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Clock,
  Copy,
  Database,
  Eye,
  EyeOff,
  Inbox,
  KeyRound,
  ListChecks,
  Mail,
  Play,
  Plus,
  RefreshCcw,
  Save,
  Search,
  ShieldCheck,
  Trash2,
  X,
  Zap
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle
} from '@/components/ui/sheet';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from '@/components/ui/table';
import { TooltipProvider } from '@/components/ui/tooltip';
import './styles.css';

type Account = {
  account_id: string;
  email: string;
  password: string;
  status: string;
  error_message: string;
  session_token: string;
  access_token: string;
  plus_trial_eligible?: boolean;
  plus_active?: boolean;
  tier: string;
  created_at: number;
  updated_at: number;
};

type CheckoutLinkResponse = {
  success: boolean;
  error_message?: string;
  checkout_url: string;
  checkout_session_id: string;
};

type Job = {
  job_id: string;
  account_id: string;
  action: string;
  status: string;
  recoverable: boolean;
  retryable: boolean;
  last_step: string;
  error_message: string;
  result_json: string;
  created_at: number;
  updated_at: number;
  steps?: Step[];
};

type WorkflowProgress = {
  job_id: string;
  workflow: string;
  step_name: string;
  status: string;
  error_message: string;
  updated_at_unix: number;
};

type JobSnapshot = {
  job?: Job;
  progress?: WorkflowProgress;
  event_id: number;
};

function snapshotEventID(snapshot: JobSnapshot) {
  return snapshot.event_id || snapshot.job?.updated_at || snapshot.progress?.updated_at_unix || 0;
}

function isRunningSnapshot(snapshot: JobSnapshot) {
  return snapshot.job?.status === 'RUNNING';
}

function jobSnapshotMatchesStatus(snapshot: JobSnapshot, status: string) {
  return !status || snapshot.job?.status === status;
}

function mergeJobSnapshots(prev: JobSnapshot[], snapshot: JobSnapshot, include: boolean) {
  const jobID = snapshot.job?.job_id;
  if (!jobID) return prev;
  const next = prev.filter((item) => item.job?.job_id !== jobID);
  if (!include) return next;
  return [snapshot, ...next].sort((a, b) => snapshotEventID(b) - snapshotEventID(a));
}

type Mailbox = {
  email_address: string;
  password: string;
  refresh_token: string;
  access_token: string;
  status: string;
  auth_status: string;
  last_error: string;
  is_primary: boolean;
  primary_email: string;
  created_at: number;
  updated_at: number;
  latest_otp: string;
  latest_otp_subject: string;
  latest_otp_received_at_unix: number;
};

type MailboxOAuthResponse = {
  started: boolean;
  job_id: string;
  error_message: string;
};

type InboxMessage = {
  id: string;
  mailbox_email: string;
  subject: string;
  from_address: string;
  body_preview: string;
  received_at_unix: number;
  recipients: string[];
  otp: string;
};

type InboxResult = {
  mailbox?: Mailbox;
  messages?: InboxMessage[];
  error_message?: string;
};

type BanDetection = {
  account_id: string;
  email_address: string;
  mailbox_email: string;
  from_address: string;
  subject: string;
  received_at_unix: number;
  account_updated: boolean;
  error_message: string;
};

type InboxResponse = {
  results?: InboxResult[];
  mailbox_count: number;
  fetched_count: number;
  failed_count: number;
  message_count: number;
  bans?: BanDetection[];
  ban_count: number;
};

type LatestOtp = {
  otp: string;
  subject: string;
  received_at_unix: number;
};

type AccountMailboxContext = {
  account_email: string;
  primary_email: string;
  is_split: boolean;
  known: boolean;
};

type Step = {
  step_name: string;
  status: string;
  recoverable: boolean;
  retryable: boolean;
  error_message: string;
  result_json: string;
  started_at: number;
  completed_at: number;
};

type Toast = { kind: 'ok' | 'error'; text: string } | null;
type ViewKey = 'accounts' | 'gopay' | 'mailboxes' | 'mailboxRegistration' | 'jobs';
type MailboxDetailTab = 'overview' | 'aliases' | 'inbox';
type DisplayLabelMap = Record<string, string>;
type PanelState = { loading: boolean; error: string };
type RowActionDescriptor = {
  label: string;
  icon: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  kind?: 'primary' | 'secondary' | 'danger';
};

const statusOptions = ['', 'CREATED', 'REGISTERED', 'ACTIVATED', 'DEACTIVATED', 'USER_ALREADY_EXISTS', 'REGISTER_FAILED', 'PAYMENT_FAILED'];
const jobStatusOptions = ['', 'RUNNING', 'SUCCEEDED', 'FAILED_RETRYABLE', 'FAILED_RECOVERABLE', 'FAILED_FINAL'];
const mailboxStatusOptions = ['', 'AVAILABLE', 'ASSIGNED', 'REGISTERED', 'USER_ALREADY_EXISTS', 'REGISTRATION_FAILED', 'BLOCKED', 'AUTHORIZED', 'OAUTH_PENDING', 'AUTH_FAILED', 'NEEDS_MANUAL_VERIFICATION'];
const mailboxUsageStatusOptions = ['AVAILABLE', 'ASSIGNED', 'REGISTERED', 'USER_ALREADY_EXISTS', 'REGISTRATION_FAILED', 'BLOCKED'];

const accountStatusLabels: DisplayLabelMap = {
  CREATED: '已创建',
  REGISTERED: '已注册',
  ACTIVATED: '已激活',
  DEACTIVATED: '已停用',
  USER_ALREADY_EXISTS: '用户已存在',
  EMAIL_ALREADY_EXISTS: '用户已存在',
  REGISTER_FAILED: '注册失败',
  PAYMENT_FAILED: '支付失败'
};

const jobStatusLabels: DisplayLabelMap = {
  RUNNING: '运行中',
  SUCCEEDED: '成功',
  FAILED_RETRYABLE: '失败',
  FAILED_RECOVERABLE: '失败，需处理',
  FAILED_FINAL: '最终失败'
};

const mailboxStatusLabels: DisplayLabelMap = {
  AVAILABLE: '可用',
  ASSIGNED: '已分配',
  REGISTERED: '已注册',
  USER_ALREADY_EXISTS: '用户已存在',
  REGISTRATION_FAILED: '注册失败',
  BLOCKED: '停止分配',
  AUTHORIZED: '已授权',
  OAUTH_PENDING: '待 OAuth',
  AUTH_FAILED: '认证失败',
  NEEDS_MANUAL_VERIFICATION: '需人工验证'
};

const actionLabels: DisplayLabelMap = {
  REGISTER: '注册账号',
  LOGIN_SESSION: '登录取 Token',
  ACTIVATE: '激活支付',
  AUTOPAY: '自动支付',
  GOPAY_APP: 'GoPay App',
  REGISTER_AND_ACTIVATE: '注册并激活',
  PROBE_ACCOUNT: '探测账号',
  REGISTER_MAILBOX: '注册 Outlook 邮箱',
  MAILBOX_OAUTH: 'Microsoft OAuth'
};

const stepLabels: DisplayLabelMap = {
  register_account: '注册账号',
  login_session: '登录取 Token',
  ensure_logon: '确认登录',
  create_email: '创建邮箱',
  wait_outlook_otp: '等待 Outlook OTP',
  gopay_app_login: 'GoPay 登录',
  gopay_app_change_phone: 'GoPay 换绑',
  gopay_app_change_phone_start: '开始换绑',
  gopay_app_change_phone_sms_wait: '等待换绑短信',
  gopay_app_change_phone_retry: '重发换绑短信',
  gopay_app_change_phone_cancel: '取消换绑号码',
  gopay_app_change_phone_complete: '完成换绑',
  gopay_app_deactivate: 'GoPay 注销',
  gopay_app_deactivate_start: '开始注销',
  gopay_app_deactivate_sms_wait: '等待注销短信',
  gopay_app_deactivate_sms_finish: '结束注销号码',
  gopay_app_deactivate_complete: '完成注销',
  gopay_app_signup: 'GoPay 注册',
  gopay_app_create_pin: '创建 GoPay PIN',
  gopay_login: 'GoPay 登录',
  gopay_payment: 'GoPay 支付',
  probe_plus_trial: '探测 0 元资格',
  probe_tier: '探测套餐',
  register_mailbox: '注册邮箱',
  mailbox_oauth: '邮箱 OAuth',
  oauth_exchange: '交换 OAuth Token',
  captcha: '验证码/风控验证'
};

function App() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [jobSnapshots, setJobSnapshots] = useState<JobSnapshot[]>([]);
  const [runningJobSnapshots, setRunningJobSnapshots] = useState<JobSnapshot[]>([]);
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [activeView, setActiveView] = useState<ViewKey>('accounts');
  const [selectedAccount, setSelectedAccount] = useState<Account | null>(null);
  const [selectedJobSnapshot, setSelectedJobSnapshot] = useState<JobSnapshot | null>(null);
  const [selectedMailbox, setSelectedMailbox] = useState<Mailbox | null>(null);
  const [accountStatus, setAccountStatus] = useState('');
  const [jobStatus, setJobStatus] = useState('');
  const [mailboxStatus, setMailboxStatus] = useState('');
  const [busy, setBusy] = useState(false);
  const [toast, setToast] = useState<Toast>(null);
  const [showSecrets, setShowSecrets] = useState(false);
  const [mailboxRegistering, setMailboxRegistering] = useState(false);
  const [mailboxOAuthing, setMailboxOAuthing] = useState('');
  const [inboxLoading, setInboxLoading] = useState(false);
  const [inboxResponse, setInboxResponse] = useState<InboxResponse | null>(null);
  const [refreshingAccessTokenIds, setRefreshingAccessTokenIds] = useState<Set<string>>(new Set());
  const [loadError, setLoadError] = useState('');
  const jobs = jobSnapshots.map((snapshot) => snapshot.job).filter((job): job is Job => !!job);
  const runningJobs = runningJobSnapshots.map((snapshot) => snapshot.job).filter((job): job is Job => !!job);
  const runningJobCount = runningJobs.length;
  const runningAccountIds = new Set(runningJobs.filter((job) => job.account_id).map((job) => job.account_id));
  const selectedJob = selectedJobSnapshot?.job || null;
  const selectedJobProgress = selectedJobSnapshot?.progress || null;
  const runningJobIDsKey = runningJobs.map((job) => job.job_id).sort().join('|');

  const applyJobSnapshot = useCallback((snapshot: JobSnapshot) => {
    if (!snapshot?.job?.job_id) return;
    setJobSnapshots((prev) => mergeJobSnapshots(prev, snapshot, jobSnapshotMatchesStatus(snapshot, jobStatus)));
    setRunningJobSnapshots((prev) => mergeJobSnapshots(prev, snapshot, isRunningSnapshot(snapshot)));
    setSelectedJobSnapshot((prev) => prev?.job?.job_id === snapshot.job?.job_id ? snapshot : prev);
  }, [jobStatus]);

  async function refresh() {
    setBusy(true);
    try {
      const [accountsData, jobsData, mailboxesData, runningJobsData] = await Promise.all([
        api<Account[]>(`/api/accounts?limit=200${accountStatus ? `&status=${accountStatus}` : ''}`),
        api<JobSnapshot[]>(`/api/jobs?limit=200${jobStatus ? `&status=${jobStatus}` : ''}`),
        api<Mailbox[]>('/api/mailboxes?limit=500'),
        api<JobSnapshot[]>('/api/jobs?limit=200&status=RUNNING')
      ]);
      setAccounts(Array.isArray(accountsData) ? accountsData : []);
      setJobSnapshots(Array.isArray(jobsData) ? jobsData : []);
      setRunningJobSnapshots(Array.isArray(runningJobsData) ? runningJobsData : []);
      const nextMailboxes = Array.isArray(mailboxesData) ? mailboxesData : [];
      setMailboxes(nextMailboxes);
      if (selectedJob) {
        await refreshSelectedJob(selectedJob.job_id);
      }
      if (selectedMailbox) {
        const freshMailbox = nextMailboxes.find((mailbox) => mailbox.email_address === selectedMailbox.email_address);
        if (freshMailbox) setSelectedMailbox(freshMailbox);
      }
      setLoadError('');
    } catch (err) {
      const message = errorText(err);
      setLoadError(message);
      setToast({ kind: 'error', text: message });
    } finally {
      setBusy(false);
    }
  }

  async function refreshSelectedJob(jobID: string) {
    const snapshot = await api<JobSnapshot>(`/api/jobs/${jobID}`);
    applyJobSnapshot(snapshot);
    setSelectedJobSnapshot(snapshot && snapshot.job ? snapshot : null);
  }

  async function runAccountWorkflow(label: string, path: string, account: Account) {
    setBusy(true);
    try {
      const resp = await api<any>(path, { method: 'POST', body: JSON.stringify({ account_id: account.account_id }) });
      if (resp.error_message) {
        setToast({ kind: 'error', text: resp.error_message });
      } else {
        setToast({ kind: 'ok', text: `${label} 已提交: ${resp.job_id || 'ok'}` });
        await refresh();
      }
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setBusy(false);
    }
  }

  async function createCheckoutLink(account: Account) {
    setBusy(true);
    try {
      const resp = await api<CheckoutLinkResponse>(`/api/accounts/${account.account_id}/checkout-link`, { method: 'POST', body: '{}' });
      if (resp.error_message || !resp.checkout_url) {
        setToast({ kind: 'error', text: resp.error_message || '提链失败' });
        return;
      }
      const copied = await copyText(resp.checkout_url);
      setToast({
        kind: copied ? 'ok' : 'error',
        text: copied ? `提链已复制: ${short(resp.checkout_session_id || 'ok')}` : '提链成功，但浏览器拒绝复制'
      });
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setBusy(false);
    }
  }

  async function copyField(label: string, value: string) {
    const copied = await copyText(value);
    setToast({
      kind: copied ? 'ok' : 'error',
      text: copied ? `${label}已复制` : `${label}复制失败，浏览器拒绝访问剪贴板`
    });
  }

  async function runGoPayApp() {
    setBusy(true);
    try {
      const resp = await api<any>('/api/workflows/gopay-app', { method: 'POST', body: '{}' });
      if (resp.error_message) {
        setToast({ kind: 'error', text: resp.error_message });
      } else {
        setToast({ kind: 'ok', text: `GoPay App 已提交: ${resp.job_id || 'ok'}` });
        await refresh();
      }
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setBusy(false);
    }
  }

  async function deleteAccount(account: Account) {
    if (!window.confirm(`删除账号 ${account.email || account.account_id}？`)) return;
    setBusy(true);
    try {
      await api<any>(`/api/accounts/${account.account_id}`, { method: 'DELETE' });
      if (selectedAccount?.account_id === account.account_id) setSelectedAccount(null);
      setToast({ kind: 'ok', text: '账号已删除' });
      await refresh();
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setBusy(false);
    }
  }

  async function submitJobOtp(job: Job, otp: string) {
    try {
      const resp = await api<{ success: boolean; job_id: string; error_message?: string }>(`/api/jobs/${job.job_id}/otp`, {
        method: 'POST',
        body: JSON.stringify({ otp })
      });
      if (resp.error_message || !resp.success) {
        setToast({ kind: 'error', text: resp.error_message || 'OTP 提交失败' });
        return;
      }
      setToast({ kind: 'ok', text: `OTP 已提交: ${short(resp.job_id || job.job_id)}` });
      await refresh();
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
      throw err;
    }
  }

  async function startMailboxRegistration() {
    setMailboxRegistering(true);
    try {
      const resp = await api<{ started: boolean }>('/api/mailboxes/register', { method: 'POST', body: '{}' });
      setToast({ kind: resp.started ? 'ok' : 'error', text: resp.started ? '手动注册邮箱已启动' : '手动注册邮箱未启动' });
      if (resp.started) await refresh();
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setMailboxRegistering(false);
    }
  }

  async function runMailboxOAuth(emailAddress = '') {
    setMailboxOAuthing(emailAddress || '*');
    try {
      const resp = await api<MailboxOAuthResponse>('/api/mailboxes/oauth', {
        method: 'POST',
        body: JSON.stringify({
          email_address: emailAddress,
          only_missing: !emailAddress,
          limit: 100
        })
      });
      if (!resp.started || resp.error_message) {
        setToast({ kind: 'error', text: resp.error_message || 'OAuth 流程启动失败' });
      } else {
        setToast({ kind: 'ok', text: `OAuth 流程已提交: ${short(resp.job_id)}` });
      }
      await refresh();
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setMailboxOAuthing('');
    }
  }

  async function fetchMailboxInbox(emailAddress = '') {
    const targetEmail = emailAddress.trim();
    setInboxLoading(true);
    try {
      const resp = await api<InboxResponse>('/api/mailboxes/inbox', {
        method: 'POST',
        body: JSON.stringify({
          limit_per_mailbox: 10,
          max_mailboxes: targetEmail ? 1 : 200,
          email_address: targetEmail
        })
      });
      setInboxResponse(resp);
      const kind = resp.failed_count > 0 ? 'error' : 'ok';
      const banText = resp.ban_count > 0 ? `，封禁 ${resp.ban_count}` : '';
      const scope = targetEmail ? `${showSecrets ? targetEmail : maskEmail(targetEmail)} ` : '';
      const latestOtp = targetEmail ? latestOtpForEmail(resp, mailboxes, targetEmail) : null;
      const otpText = latestOtp
        ? `，OTP ${showSecrets ? latestOtp.otp : mask(latestOtp.otp)}，${formatUnix(latestOtp.received_at_unix)}`
        : '';
      setToast({ kind, text: `${scope}收信完成：${resp.fetched_count}/${resp.mailbox_count} 个邮箱，${resp.message_count} 封邮件${otpText}${banText}` });
      await refresh();
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setInboxLoading(false);
    }
  }

  async function deleteMailbox(mailbox: Mailbox) {
    const email = mailbox.email_address;
    if (!email) return;
    const label = showSecrets ? email : maskEmail(email);
    const message = mailbox.is_primary ? `删除主邮箱 ${label} 及其 Alias？` : `删除 Alias ${label}？`;
    if (!window.confirm(message)) return;
    setBusy(true);
    try {
      await api<{ deleted: boolean }>(`/api/mailboxes/${encodeURIComponent(email)}`, { method: 'DELETE' });
      setToast({ kind: 'ok', text: `邮箱已删除: ${label}` });
      if (selectedMailbox?.email_address === email || (mailbox.is_primary && selectedMailbox?.primary_email === email)) {
        closeDetails();
      }
      await refresh();
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setBusy(false);
    }
  }

  async function updateAccountAuth(account: Account, payload: { session_token?: string; access_token?: string }) {
    setBusy(true);
    try {
      const updated = await api<Account>(`/api/accounts/${account.account_id}`, {
        method: 'PATCH',
        body: JSON.stringify(payload)
      });
      setAccounts((prev) => prev.map((item) => item.account_id === updated.account_id ? updated : item));
      setSelectedAccount(updated);
      setToast({ kind: 'ok', text: '认证信息已更新' });
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
      throw err;
    } finally {
      setBusy(false);
    }
  }

  async function refreshAccountAccessToken(account: Account) {
    setRefreshingAccessTokenIds((prev) => new Set(prev).add(account.account_id));
    try {
      const updated = await api<Account>(`/api/accounts/${account.account_id}/access-token`, {
        method: 'POST',
        body: '{}'
      });
      setAccounts((prev) => prev.map((item) => item.account_id === updated.account_id ? updated : item));
      if (selectedAccount?.account_id === updated.account_id) setSelectedAccount(updated);
      setToast({ kind: 'ok', text: 'Access Token 已自动获取' });
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
      throw err;
    } finally {
      setRefreshingAccessTokenIds((prev) => {
        const next = new Set(prev);
        next.delete(account.account_id);
        return next;
      });
    }
  }

  useEffect(() => {
    refresh();
  }, [accountStatus, jobStatus, mailboxStatus]);

  useEffect(() => {
    if (!runningJobIDsKey) {
      return;
    }
    const sources = runningJobIDsKey.split('|').map((jobID) => {
      const source = new EventSource(`/api/jobs/${jobID}/events`);
      source.addEventListener('job', (event) => {
        const snapshot = JSON.parse((event as MessageEvent).data) as JobSnapshot;
        applyJobSnapshot(snapshot);
        if (snapshot.job && snapshot.job.status !== 'RUNNING') {
          source.close();
        }
      });
      source.addEventListener('error', (event) => {
        const data = (event as MessageEvent).data;
        if (!data) return;
        try {
          const payload = JSON.parse(data) as { error?: string };
          if (payload.error) setToast({ kind: 'error', text: payload.error });
        } catch {
          setToast({ kind: 'error', text: '工作流事件流解析失败' });
        }
        source.close();
      });
      return source;
    });
    return () => {
      sources.forEach((source) => source.close());
    };
  }, [runningJobIDsKey, applyJobSnapshot]);

  useEffect(() => {
    if (!toast) return;
    const id = window.setTimeout(() => setToast(null), toast.kind === 'error' ? 6000 : 3500);
    return () => window.clearTimeout(id);
  }, [toast]);

  function selectAccount(account: Account) {
    setSelectedAccount(account);
    setSelectedJobSnapshot(null);
    setSelectedMailbox(null);
  }

  async function selectJob(job: Job) {
    try {
      setSelectedAccount(null);
      setSelectedMailbox(null);
      await refreshSelectedJob(job.job_id);
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    }
  }

  function selectMailbox(mailbox: Mailbox) {
    setSelectedAccount(null);
    setSelectedJobSnapshot(null);
    setSelectedMailbox(mailbox);
  }

  function closeDetails() {
    setSelectedAccount(null);
    setSelectedJobSnapshot(null);
    setSelectedMailbox(null);
  }

  function openView(view: ViewKey) {
    setActiveView(view);
    closeDetails();
  }

  const primaryMailboxes = mailboxes.filter((mailbox) => mailbox.is_primary);
  const visiblePrimaryMailboxes = primaryMailboxes.filter((mailbox) => mailboxMatchesFilter(mailbox, mailboxes, mailboxStatus));
  const allocatableMailboxCount = primaryMailboxes.filter((m) => m.status === 'AVAILABLE' && authStatus(m) === 'AUTHORIZED').length;
  const missingOAuthCount = primaryMailboxes.filter((mailbox) => (
    mailbox.password && authStatus(mailbox) !== 'AUTHORIZED' && authStatus(mailbox) !== 'NEEDS_MANUAL_VERIFICATION'
  )).length;
  const oauthMailboxCount = primaryMailboxes.filter((mailbox) => authStatus(mailbox) === 'AUTHORIZED').length;
  const selectedMailboxInbox = selectedMailbox ? inboxResultForMailbox(inboxResponse, selectedMailbox.email_address) : undefined;
  const selectedMailboxBans = selectedMailbox ? bansForMailbox(inboxResponse, selectedMailbox.email_address) : [];
  const selectedMailboxAliases = selectedMailbox ? aliasesForMailbox(mailboxes, selectedMailbox) : [];
  const selectedAccountMailboxContext = selectedAccount ? mailboxContextForEmail(mailboxes, selectedAccount.email) : null;
  const mailboxRegisterJobs = jobs.filter((job) => job.action === 'REGISTER_MAILBOX');
  const runningMailboxRegisterCount = mailboxRegisterJobs.filter((job) => job.status === 'RUNNING').length;
  const gopayAppJobs = jobs.filter((job) => job.action === 'GOPAY_APP');
  const runningGoPayAppCount = gopayAppJobs.filter((job) => job.status === 'RUNNING').length;
  const latestGoPayAppJob = gopayAppJobs[0];
  const latestMailboxRegisterJob = mailboxRegisterJobs[0];
  const panelState: PanelState = {
    loading: busy && accounts.length === 0 && jobs.length === 0 && mailboxes.length === 0,
    error: loadError
  };

  return (
    <main className="shell">
      <header className="topbar">
        <div>
          <h1>NB Register</h1>
          <p>账号、注册、激活和 GoPay 工作流控制台</p>
        </div>
        <div className="topbarActions">
          <Button className="primaryButton" onClick={refresh} disabled={busy}>
            <RefreshCcw size={16} /> 刷新
          </Button>
        </div>
      </header>

      {toast && <div className={`toast ${toast.kind}`} title={toast.text}>{compactToast(toast.text)}</div>}

      <section className="appFrame">
        <nav className="navRail" aria-label="主导航">
          <NavItem active={activeView === 'accounts'} icon={<Database size={17} />} label="账号" count={accounts.length} countLabel="全部账号数" onClick={() => openView('accounts')} />
          <NavItem active={activeView === 'gopay'} icon={<RefreshCcw size={17} />} label="GoPay" count={runningGoPayAppCount} countLabel="运行中的 GoPay App 任务" onClick={() => openView('gopay')} />
          <NavItem active={activeView === 'mailboxes'} icon={<Inbox size={17} />} label="邮箱管理" count={allocatableMailboxCount} countLabel="可分配主邮箱数" onClick={() => openView('mailboxes')} />
          <NavItem active={activeView === 'mailboxRegistration'} icon={<Play size={17} />} label="邮箱注册" count={runningMailboxRegisterCount} countLabel="运行中的邮箱注册任务" onClick={() => openView('mailboxRegistration')} />
          <NavItem active={activeView === 'jobs'} icon={<ListChecks size={17} />} label="工作流" count={runningJobCount} countLabel="运行中的工作流任务" onClick={() => openView('jobs')} />
        </nav>

        <div className="contentPane">
          {activeView === 'accounts' && (
            <section className="metrics">
              <Metric label="账号" value={accounts.length} hint="当前账号池总量" icon={<ShieldCheck />} />
              <Metric label="已激活" value={accounts.filter((a) => a.status === 'ACTIVATED').length} hint="可进入后续使用的账号" icon={<Zap />} />
              <Metric label="可分配邮箱" value={allocatableMailboxCount} hint="AVAILABLE 且 OAuth 已授权" icon={<Mail />} />
              <Metric label="运行中任务" value={runningJobCount} hint="正在执行的工作流" icon={<Activity />} />
            </section>
          )}

          <div className="contentStatus">
            {panelState.error && <PanelNotice kind="error" title="数据刷新失败" text={panelState.error} />}
            {panelState.loading && <PanelNotice kind="info" title="正在加载" text="正在刷新账号、邮箱和工作流数据。" />}
          </div>

          {activeView === 'accounts' && (
            <section className="workspace accountsWorkspace">
              <div className="panel accountsPanel">
                <PanelHeader title="账号管理" icon={<Search size={16} />}>
                  <div className="headerControls">
                    <Button className="secondaryButton" onClick={() => setShowSecrets((v) => !v)}>
                      {showSecrets ? <EyeOff size={16} /> : <Eye size={16} />}
                      {showSecrets ? '隐藏' : '显示'}
                    </Button>
                    <NativeSelect value={accountStatus} onChange={(e) => setAccountStatus(e.target.value)}>
                      {statusOptions.map((s) => <NativeSelectOption key={s} value={s}>{s ? statusText(s) : '全部状态'}</NativeSelectOption>)}
                    </NativeSelect>
                  </div>
                </PanelHeader>
                <PanelIntro text="创建账号时邮箱和密码可留空；系统会从邮箱池取可用邮箱，并自动生成密码。" />
                <CreateAccountForm
                  onDone={async (message) => {
                    setToast({ kind: 'ok', text: message });
                    await refresh();
                  }}
                  onError={(message) => setToast({ kind: 'error', text: message })}
                />
                <AccountTable
                  accounts={accounts}
                  selected={selectedAccount?.account_id}
                  showSecrets={showSecrets}
                  runningAccountIds={runningAccountIds}
                  refreshingAccessTokenIds={refreshingAccessTokenIds}
                  busy={busy}
                  onSelect={selectAccount}
                  onRegister={(account) => runAccountWorkflow('注册账号', '/api/workflows/register', account)}
                  onLogin={(account) => runAccountWorkflow(loginActionLabel(account), '/api/workflows/login', account)}
                  onActivate={(account) => runAccountWorkflow('激活支付', '/api/workflows/activate', account)}
	                  onAutopay={(account) => runAccountWorkflow('自动支付', '/api/workflows/autopay', account)}
	                  onCreateCheckoutLink={createCheckoutLink}
	                  onProbeAccount={(account) => runAccountWorkflow('探测账号', '/api/workflows/probe', account)}
                  onRegisterActivate={(account) => runAccountWorkflow('注册并激活', '/api/workflows/register-and-activate', account)}
                  onRefreshAccessToken={refreshAccountAccessToken}
                  onDelete={deleteAccount}
                />
              </div>

              <div className="panel jobsPanel compactPanel">
                <PanelHeader title="最近工作流" icon={<Activity size={16} />}>
                  <Button className="secondaryButton" onClick={() => openView('jobs')}>查看全部</Button>
                </PanelHeader>
                <JobCompactList jobs={jobs.slice(0, 8)} selected={selectedJob?.job_id} onSelect={selectJob} />
              </div>
            </section>
          )}

          {activeView === 'gopay' && (
            <section className="workspace jobsWorkspace">
              <div className="panel jobsPanel">
                <PanelHeader title="GoPay App" icon={<RefreshCcw size={16} />}>
                  <div className="headerControls">
                    <Button className="primaryButton" onClick={runGoPayApp} disabled={busy || runningGoPayAppCount > 0}>
                      <RefreshCcw size={16} /> {runningGoPayAppCount > 0 ? '执行中' : '启动'}
                    </Button>
                    <Button className="secondaryButton" onClick={() => openView('jobs')}>
                      <ListChecks size={14} /> 全部工作流
                    </Button>
                  </div>
                </PanelHeader>
                <PanelIntro text="登录、换绑、注销、注册和建 PIN 按工作流顺序执行。" />
                <div className="mailboxRegisterBody">
                  <RegistrationSummary job={latestGoPayAppJob} runningCount={runningGoPayAppCount} />
                  {latestGoPayAppJob && canSubmitOtp(latestGoPayAppJob) && (
                    <OtpSubmitter job={latestGoPayAppJob} onSubmit={submitJobOtp} />
                  )}
                  <JobTable jobs={gopayAppJobs.slice(0, 20)} selected={selectedJob?.job_id} emptyText="暂无 GoPay App 任务" onSelect={selectJob} />
                </div>
              </div>
            </section>
          )}

          {activeView === 'mailboxes' && (
            <section className="workspace mailboxWorkspace">
              <div className="panel mailboxesPanel">
                <PanelHeader title="邮箱管理" icon={<Mail size={16} />}>
                  <div className="headerControls">
                    <Button className="secondaryButton" onClick={() => runMailboxOAuth()} disabled={busy || !!mailboxOAuthing || missingOAuthCount === 0}>
                      <KeyRound size={16} /> 补 OAuth {missingOAuthCount > 0 ? `(${missingOAuthCount})` : ''}
                    </Button>
                    <Button className="secondaryButton" onClick={() => fetchMailboxInbox()} disabled={busy || inboxLoading || oauthMailboxCount === 0}>
                      <Inbox size={16} /> {inboxLoading ? '拉取中' : `批量收信${oauthMailboxCount > 0 ? ` (${oauthMailboxCount})` : ''}`}
                    </Button>
                    <Button className="secondaryButton" onClick={() => setShowSecrets((v) => !v)}>
                      {showSecrets ? <EyeOff size={16} /> : <Eye size={16} />}
                      {showSecrets ? '隐藏' : '显示'}
                    </Button>
                    <NativeSelect value={mailboxStatus} onChange={(e) => setMailboxStatus(e.target.value)}>
                      {mailboxStatusOptions.map((s) => <NativeSelectOption key={s} value={s}>{s ? statusText(s) : '全部状态'}</NativeSelectOption>)}
                    </NativeSelect>
                  </div>
                </PanelHeader>
                <MailboxPanel
                  mailboxes={visiblePrimaryMailboxes}
                  allMailboxes={primaryMailboxes}
                  selected={selectedMailbox?.email_address}
                  busy={busy}
                  showSecrets={showSecrets}
                  oauthing={mailboxOAuthing}
                  onSelect={selectMailbox}
                  onOAuth={runMailboxOAuth}
                  onDelete={deleteMailbox}
                  onDone={async (message) => {
                    setToast({ kind: 'ok', text: message });
                    await refresh();
                  }}
                  onError={(message) => setToast({ kind: 'error', text: message })}
                />
	              </div>
	            </section>
	          )}

	          {activeView === 'mailboxRegistration' && (
	            <section className="workspace mailboxRegistrationWorkspace">
	              <div className="panel mailboxRegisterPanel">
	                <PanelHeader title="邮箱注册" icon={<Play size={16} />}>
	                  <div className="headerControls">
	                    <Button className="primaryButton" onClick={startMailboxRegistration} disabled={busy || mailboxRegistering}>
	                      <Play size={16} /> {mailboxRegistering ? '启动中' : '启动注册'}
	                    </Button>
	                    <Button className="secondaryButton" onClick={() => openView('mailboxes')}>
	                      <Inbox size={16} /> 邮箱管理
	                    </Button>
	                  </div>
	                </PanelHeader>
	                <div className="mailboxRegisterBody">
                    <RegistrationSummary job={latestMailboxRegisterJob} runningCount={runningMailboxRegisterCount} />
	                  <MailboxStatusStrip mailboxes={primaryMailboxes} />
	                  <div className="sectionTitle">
	                    <h3>邮箱注册工作流</h3>
	                    <Button className="secondaryButton" onClick={() => openView('jobs')}>
	                      <ListChecks size={14} /> 全部工作流
	                    </Button>
	                  </div>
	                  <JobTable jobs={mailboxRegisterJobs.slice(0, 20)} selected={selectedJob?.job_id} emptyText="暂无邮箱注册任务" onSelect={selectJob} />
	                </div>
	              </div>
	            </section>
	          )}

	          {activeView === 'jobs' && (
            <section className="workspace jobsWorkspace">
              <div className="panel jobsPanel">
                <PanelHeader title="工作流" icon={<Activity size={16} />}>
                  <NativeSelect value={jobStatus} onChange={(e) => setJobStatus(e.target.value)}>
                    {jobStatusOptions.map((s) => <NativeSelectOption key={s} value={s}>{s ? statusText(s) : '全部状态'}</NativeSelectOption>)}
                  </NativeSelect>
                </PanelHeader>
                <JobTable jobs={jobs} selected={selectedJob?.job_id} emptyText="暂无工作流任务" onSelect={selectJob} />
              </div>
            </section>
          )}
        </div>
      </section>

      <DetailDrawer open={!!selectedAccount} title="账号详情" onClose={closeDetails}>
        {selectedAccount && (
          <AccountDetails
            account={selectedAccount}
            showSecrets={showSecrets}
            busy={busy}
            inboxLoading={inboxLoading}
            mailboxContext={selectedAccountMailboxContext}
            onCopy={copyField}
            onFetchInbox={fetchMailboxInbox}
            onSessionSave={(account, sessionToken) => updateAccountAuth(account, { session_token: sessionToken })}
            onAccessSave={(account, accessToken) => updateAccountAuth(account, { access_token: accessToken })}
	            onProbeAccount={(account) => runAccountWorkflow('探测账号', '/api/workflows/probe', account)}
	            onCreateCheckoutLink={createCheckoutLink}
	            onLogin={(account) => runAccountWorkflow(loginActionLabel(account), '/api/workflows/login', account)}
            onRefreshAccessToken={refreshAccountAccessToken}
            refreshingAccessToken={refreshingAccessTokenIds.has(selectedAccount.account_id)}
          />
        )}
      </DetailDrawer>

      <DetailDrawer open={!!selectedJob} title="工作流详情" onClose={closeDetails}>
        {selectedJob && (
          <JobDetails
            job={selectedJob}
            progress={selectedJobProgress}
            onCopy={copyField}
            onOtpSubmit={submitJobOtp}
          />
        )}
      </DetailDrawer>

      <DetailDrawer open={!!selectedMailbox} title="邮箱详情" onClose={closeDetails}>
        {selectedMailbox && (
          <MailboxDetails
            mailbox={selectedMailbox}
            showSecrets={showSecrets}
            inboxResult={selectedMailboxInbox}
            bans={selectedMailboxBans}
            aliases={selectedMailboxAliases}
            inboxLoading={inboxLoading}
            onCopy={copyField}
            onFetchInbox={fetchMailboxInbox}
            onDelete={deleteMailbox}
          />
        )}
      </DetailDrawer>
    </main>
  );
}

function NavItem({ active, icon, label, count, countLabel, onClick }: {
  active: boolean;
  icon: React.ReactNode;
  label: string;
  count: number;
  countLabel: string;
  onClick: () => void;
}) {
  return (
    <Button className={`navItem ${active ? 'active' : ''}`} title={`${label}：${countLabel} ${count}`} onClick={onClick}>
      <span>{icon}</span>
      <strong>{label}</strong>
      <em aria-label={`${countLabel}: ${count}`}>{count}</em>
    </Button>
  );
}

function Metric({ label, value, hint, icon }: { label: string; value: number; hint: string; icon: React.ReactNode }) {
  return (
    <Card className="metric" title={hint}>
      <CardContent className="metricContent">
        <span>{icon}</span>
        <div>
          <strong>{value}</strong>
          <p>{label}</p>
          <small>{hint}</small>
        </div>
      </CardContent>
    </Card>
  );
}

function PanelHeader({ title, icon, children }: { title: string; icon: React.ReactNode; children?: React.ReactNode }) {
  return (
    <div className="panelHeader">
      <div><span>{icon}</span>{title}</div>
      {children}
    </div>
  );
}

function PanelIntro({ text }: { text: string }) {
  return <div className="panelIntro">{text}</div>;
}

function PanelNotice({ kind, title, text }: { kind: 'info' | 'error'; title: string; text: string }) {
  return (
    <div className={`panelNotice ${kind}`} role={kind === 'error' ? 'alert' : 'status'}>
      {kind === 'error' ? <AlertTriangle size={16} /> : <Clock size={16} />}
      <div>
        <strong>{title}</strong>
        <span>{text}</span>
      </div>
    </div>
  );
}

function EmptyTableRow({ colSpan, text }: { colSpan: number; text: string }) {
  return (
    <TableRow className="emptyTableRow">
      <TableCell colSpan={colSpan}>
        <EmptyBlock text={text} />
      </TableCell>
    </TableRow>
  );
}

function EmptyBlock({ text }: { text: string }) {
  return <div className="emptyBlock">{text}</div>;
}

function DetailDrawer({ open, title, onClose, children }: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <Sheet open={open} onOpenChange={(nextOpen) => {
      if (!nextOpen) onClose();
    }}>
      <SheetContent className="detailDrawer" side="right" showCloseButton>
        <SheetHeader className="drawerHeader">
          <SheetTitle className="drawerTitle"><Activity size={16} />{title}</SheetTitle>
          <SheetDescription className="sr-only">{title}明细面板</SheetDescription>
        </SheetHeader>
        <div className="drawerBody">
          {children}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function AccountDetails({ account, showSecrets, busy, inboxLoading, refreshingAccessToken, mailboxContext, onCopy, onFetchInbox, onSessionSave, onAccessSave, onProbeAccount, onCreateCheckoutLink, onLogin, onRefreshAccessToken }: {
  account: Account;
  showSecrets: boolean;
  busy: boolean;
  inboxLoading: boolean;
  refreshingAccessToken: boolean;
  mailboxContext: AccountMailboxContext | null;
  onCopy: (label: string, value: string) => void;
  onFetchInbox: (emailAddress?: string) => Promise<void>;
  onSessionSave: (account: Account, sessionToken: string) => Promise<void>;
  onAccessSave: (account: Account, accessToken: string) => Promise<void>;
  onProbeAccount: (account: Account) => void;
  onCreateCheckoutLink: (account: Account) => Promise<void>;
  onLogin: (account: Account) => void;
  onRefreshAccessToken: (account: Account) => Promise<void>;
}) {
  return (
    <div className="details">
      <section>
        <div className="sectionTitle">
          <h3>账号</h3>
          <div className="sectionActions">
            {canRefreshAccessToken(account) && (
              <Button {...buttonHint('使用当前 Session 自动获取 Access Token')} disabled={busy || refreshingAccessToken} onClick={() => void onRefreshAccessToken(account)}>
                <KeyRound size={14} /> {refreshingAccessToken ? '获取中' : '自动获取 Access Token'}
              </Button>
            )}
            {canLoginSession(account) && (
              <Button {...buttonHint(loginActionHint(account))} disabled={busy} onClick={() => onLogin(account)}>
                <KeyRound size={14} /> {loginActionLabel(account)}
              </Button>
            )}
            <Button {...buttonHint(probeAccountHint(account))} disabled={busy || !canProbeAccount(account)} onClick={() => onProbeAccount(account)}>
              <Search size={14} /> 探测账号
            </Button>
            <Button {...buttonHint('创建 checkout 链接并复制到剪贴板')} disabled={busy || !canCreateCheckoutLink(account)} onClick={() => void onCreateCheckoutLink(account)}>
              <Copy size={14} /> 提链
            </Button>
            <Button {...buttonHint(accountInboxHint(account.email, mailboxContext, showSecrets))} disabled={busy || inboxLoading || !account.email} onClick={() => void onFetchInbox(account.email)}>
              <Inbox size={14} /> {inboxLoading ? '拉取中' : '拉取 OTP'}
            </Button>
          </div>
        </div>
        <KV label="ID" value={account.account_id} mono onCopy={onCopy} />
        <KV label="状态" value={statusText(account.status)} copyValue={account.status || '-'} onCopy={onCopy} />
        <KV label="Tier" value={tierText(account.tier)} />
        <KV label="Plus" value={plusText(account)} />
        <KV label="邮箱" value={showSecrets ? account.email : maskEmail(account.email)} copyValue={account.email} copyDisabled={!account.email} masked={!showSecrets} onCopy={onCopy} />
        <KV label="密码" value={showSecrets ? account.password : mask(account.password)} copyValue={account.password} copyDisabled={!account.password} masked={!showSecrets} mono onCopy={onCopy} />
        <TokenEditor label="Session" field="session_token" account={account} showSecrets={showSecrets} onCopy={onCopy} onSave={onSessionSave} />
        <TokenEditor label="Access" field="access_token" account={account} showSecrets={showSecrets} onCopy={onCopy} onSave={onAccessSave} />
        <KV label="创建时间" value={formatUnix(account.created_at)} onCopy={onCopy} />
        <KV label="更新时间" value={formatUnix(account.updated_at)} onCopy={onCopy} />
        <KV label="错误" value={account.error_message || '-'} onCopy={onCopy} />
      </section>
    </div>
  );
}

function JobDetails({ job, progress, onCopy, onOtpSubmit }: {
  job: Job;
  progress: WorkflowProgress | null;
  onCopy: (label: string, value: string) => void;
  onOtpSubmit: (job: Job, otp: string) => Promise<void>;
}) {
  return (
    <div className="details">
      <section>
        <div className="sectionTitle">
          <h3>工作流</h3>
        </div>
        <KV label="Job" value={job.job_id} mono onCopy={onCopy} />
        <KV label="对象" value={job.account_id || '-'} mono onCopy={onCopy} />
        <KV label="动作" value={actionText(job.action)} copyValue={job.action} onCopy={onCopy} />
        <KV label="状态" value={statusText(job.status)} copyValue={job.status} onCopy={onCopy} />
        <KV label="当前步骤" value={stepText(job.last_step)} copyValue={job.last_step || '-'} onCopy={onCopy} />
        {progress && (
          <>
            <KV label="执行状态" value={`${statusText(progress.status.toUpperCase())} · ${stepText(progress.step_name)}`} copyValue={progress.step_name || '-'} onCopy={onCopy} />
            <KV label="执行刷新" value={formatUnix(progress.updated_at_unix)} onCopy={onCopy} />
          </>
        )}
        <KV label="更新时间" value={formatJobTime(job.updated_at)} onCopy={onCopy} />
        <KV label="错误" value={job.error_message || '-'} onCopy={onCopy} />
        {canSubmitOtp(job) && <OtpSubmitter job={job} onSubmit={onOtpSubmit} />}
        <div className="timeline">
          {(job.steps || []).map((step) => {
            const progressText = stepProgressText(step, progress);
            const isCurrentStep = progress?.step_name === step.step_name && job.status === 'RUNNING';
            return (
              <div className={`step${isCurrentStep ? ' currentStep' : ''}`} key={step.step_name}>
                <div className="stepHeader">
                  <strong>{stepText(step.step_name)} <small className="rawHint">{step.step_name}</small></strong>
                  <span className="stepState">
                    {isCurrentStep && <small className="stepLive">当前</small>}
                    {stepDuration(step)}
                    <StatusBadge status={step.status} />
                  </span>
                </div>
                <div className="stepMeta">
                  {step.started_at ? <small>开始 {formatUnix(step.started_at)}</small> : null}
                  {step.completed_at ? <small>完成 {formatUnix(step.completed_at)}</small> : null}
                  {step.recoverable ? <small>可恢复</small> : null}
                  {step.retryable ? <small>可重试</small> : null}
                </div>
                {progressText && <p className="stepProgress">{progressText}</p>}
                {step.error_message && <p>{step.error_message}</p>}
                {step.result_json && (
                  <details className="jsonDetails">
                    <summary>结果数据</summary>
                    <pre>{formatJSON(step.result_json)}</pre>
                  </details>
                )}
              </div>
            );
          })}
          {(!job.steps || job.steps.length === 0) && <EmptyBlock text="暂无步骤明细。" />}
        </div>
      </section>
    </div>
  );
}

function OtpSubmitter({ job, onSubmit }: {
  job: Job;
  onSubmit: (job: Job, otp: string) => Promise<void>;
}) {
  const [otp, setOtp] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const label = otpSubmitLabel(job);

  async function submit() {
    const value = otp.trim();
    if (!value) return;
    setSubmitting(true);
    try {
      await onSubmit(job, value);
      setOtp('');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="otpSubmitter">
      <span><KeyRound size={14} /> {label}</span>
      <div>
        <Input
          inputMode="numeric"
          autoComplete="one-time-code"
          placeholder="验证码"
          value={otp}
          onChange={(event) => setOtp(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void submit();
          }}
        />
        <Button className="primaryButton" disabled={submitting || !otp.trim()} onClick={() => void submit()}>
          <KeyRound size={14} /> 提交
        </Button>
      </div>
    </div>
  );
}

function AccountTable({ accounts, selected, showSecrets, runningAccountIds, refreshingAccessTokenIds, busy, onSelect, onRegister, onLogin, onActivate, onAutopay, onCreateCheckoutLink, onProbeAccount, onRegisterActivate, onRefreshAccessToken, onDelete }: {
  accounts: Account[];
  selected?: string;
  showSecrets: boolean;
  runningAccountIds: Set<string>;
  refreshingAccessTokenIds: Set<string>;
  busy: boolean;
  onSelect: (a: Account) => void;
  onRegister: (a: Account) => void;
  onLogin: (a: Account) => void;
  onActivate: (a: Account) => void;
  onAutopay: (a: Account) => void;
  onCreateCheckoutLink: (a: Account) => Promise<void>;
  onProbeAccount: (a: Account) => void;
  onRegisterActivate: (a: Account) => void;
  onRefreshAccessToken: (a: Account) => Promise<void>;
  onDelete: (a: Account) => void;
}) {
  return (
    <div className="tableWrap">
      <Table className="responsiveTable accountsTable">
        <TableHeader>
          <TableRow>
            <TableHead>账号</TableHead>
            <TableHead>密码</TableHead>
            <TableHead>状态</TableHead>
            <TableHead>Tier</TableHead>
            <TableHead>Plus</TableHead>
            <TableHead>更新</TableHead>
            <TableHead>错误</TableHead>
            <TableHead>操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {accounts.length === 0 && <EmptyTableRow colSpan={8} text="暂无账号。可以先创建账号，或切换为全部状态查看。" />}
          {accounts.map((account) => {
            const accountBusy = runningAccountIds.has(account.account_id);
            const refreshingAccessToken = refreshingAccessTokenIds.has(account.account_id);
            return (
              <TableRow key={account.account_id} className={selected === account.account_id ? 'selected' : ''} onClick={() => onSelect(account)}>
                <TableCell data-label="账号">
                  <div className="cellStack">
                    <span>{showSecrets ? (account.email || '-') : maskEmail(account.email)}</span>
                    <small className="mono">{short(account.account_id)}</small>
                  </div>
                </TableCell>
                <TableCell data-label="密码" className="secret">{showSecrets ? account.password : mask(account.password)}</TableCell>
                <TableCell data-label="状态">
                  <div className="cellStack">
                    <StatusBadge status={account.status} />
                  </div>
                </TableCell>
                <TableCell data-label="Tier"><TierBadge tier={account.tier} /></TableCell>
                <TableCell data-label="Plus"><PlusBadge account={account} /></TableCell>
                <TableCell data-label="更新">{formatUnix(account.updated_at)}</TableCell>
                <TableCell data-label="错误" className="errorCell" title={account.error_message}>{compactCellError(account.error_message || '-')}</TableCell>
                <TableCell data-label="操作">
                  <AccountRowActions
                    account={account}
                    accountBusy={accountBusy}
                    busy={busy}
                    refreshingAccessToken={refreshingAccessToken}
                    onRegister={onRegister}
                    onLogin={onLogin}
                    onActivate={onActivate}
                    onAutopay={onAutopay}
                    onCreateCheckoutLink={onCreateCheckoutLink}
                    onProbeAccount={onProbeAccount}
                    onRegisterActivate={onRegisterActivate}
                    onRefreshAccessToken={onRefreshAccessToken}
                    onDelete={onDelete}
                  />
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}

function AccountRowActions({ account, accountBusy, busy, refreshingAccessToken, onRegister, onLogin, onActivate, onAutopay, onCreateCheckoutLink, onProbeAccount, onRegisterActivate, onRefreshAccessToken, onDelete }: {
  account: Account;
  accountBusy: boolean;
  busy: boolean;
  refreshingAccessToken: boolean;
  onRegister: (a: Account) => void;
  onLogin: (a: Account) => void;
  onActivate: (a: Account) => void;
  onAutopay: (a: Account) => void;
  onCreateCheckoutLink: (a: Account) => Promise<void>;
  onProbeAccount: (a: Account) => void;
  onRegisterActivate: (a: Account) => void;
  onRefreshAccessToken: (a: Account) => Promise<void>;
  onDelete: (a: Account) => void;
}) {
  if (accountBusy && !isUserAlreadyExistsAccount(account)) return <span className="busyLabel">进行中</span>;

  const actions: RowActionDescriptor[] = [];
  if (canRegister(account)) actions.push({ label: '注册账号', icon: <Play size={14} />, onClick: () => onRegister(account), disabled: busy, kind: 'primary' });
  if (canActivate(account)) actions.push({ label: '激活支付', icon: <Zap size={14} />, onClick: () => onActivate(account), disabled: true, kind: 'secondary' });
  if (canAutopay(account)) actions.push({ label: '自动支付', icon: <Zap size={14} />, onClick: () => onAutopay(account), disabled: busy, kind: actions.length ? 'secondary' : 'primary' });
  if (canRefreshAccessToken(account)) actions.push({ label: refreshingAccessToken ? '获取中' : '获取 Access', icon: <KeyRound size={14} />, onClick: () => void onRefreshAccessToken(account), disabled: busy || refreshingAccessToken, kind: actions.length ? 'secondary' : 'primary' });
  if (canLoginSession(account)) actions.push({ label: loginActionLabel(account), icon: <KeyRound size={14} />, onClick: () => onLogin(account), disabled: busy, kind: actions.length ? 'secondary' : 'primary' });
  if (canCreateCheckoutLink(account)) actions.push({ label: '提链', icon: <Copy size={14} />, onClick: () => void onCreateCheckoutLink(account), disabled: busy, kind: actions.length ? 'secondary' : 'primary' });
  if (canProbeAccount(account)) actions.push({ label: '探测账号', icon: <Search size={14} />, onClick: () => onProbeAccount(account), disabled: busy, kind: 'secondary' });
  if (canRegister(account)) actions.push({ label: '注册并激活', icon: <ShieldCheck size={14} />, onClick: () => onRegisterActivate(account), disabled: busy, kind: 'secondary' });
  actions.push({ label: '删除账号', icon: <Trash2 size={14} />, onClick: () => onDelete(account), disabled: busy, kind: 'danger' });

  const primary = actions.find((action) => action.kind === 'primary' && !action.disabled) ||
    actions.find((action) => !action.disabled) ||
    actions[0];
  const secondary = actions.filter((action) => action !== primary);

  return (
    <div className="rowActions" onClick={(event) => event.stopPropagation()}>
      <RowActionButton action={primary} showLabel />
      {secondary.map((action) => <RowActionButton key={action.label} action={action} />)}
    </div>
  );
}

function RowActionButton({ action, showLabel }: { action: RowActionDescriptor; showLabel?: boolean }) {
  const className = [
    showLabel ? 'rowButtonText' : 'iconButton',
    action.kind === 'primary' ? 'primaryRowAction' : '',
    action.kind === 'danger' ? 'dangerButton' : ''
  ].filter(Boolean).join(' ');

  return (
    <Button className={className} {...buttonHint(action.label)} disabled={action.disabled} onClick={action.onClick}>
      {action.icon}
      {showLabel && <span>{action.label}</span>}
    </Button>
  );
}

function JobTable({ jobs, selected, emptyText = '暂无工作流任务', onSelect }: {
  jobs: Job[];
  selected?: string;
  emptyText?: string;
  onSelect: (j: Job) => void;
}) {
  return (
    <div className="tableWrap">
      <Table className="responsiveTable jobTable">
        <TableHeader>
          <TableRow><TableHead>Job</TableHead><TableHead>对象</TableHead><TableHead>动作</TableHead><TableHead>状态</TableHead><TableHead>步骤</TableHead><TableHead>更新</TableHead><TableHead>错误</TableHead><TableHead>操作</TableHead></TableRow>
        </TableHeader>
        <TableBody>
          {jobs.length === 0 && <EmptyTableRow colSpan={8} text={emptyText} />}
          {jobs.map((job) => (
            <TableRow key={job.job_id} className={selected === job.job_id ? 'selected' : ''} onClick={() => onSelect(job)}>
              <TableCell data-label="Job" className="mono">
                <div className="cellStack">
                  <span>{short(job.job_id)}</span>
                  {canSubmitOtp(job) && <small className="needsOtp">需要 OTP</small>}
                </div>
              </TableCell>
              <TableCell data-label="对象" className="mono">{short(job.account_id || '-', 10)}</TableCell>
              <TableCell data-label="动作" title={job.action}>{actionText(job.action)}</TableCell>
              <TableCell data-label="状态"><StatusBadge status={job.status} /></TableCell>
              <TableCell data-label="步骤" title={job.last_step}>{stepText(job.last_step)}</TableCell>
              <TableCell data-label="更新">{formatJobTime(job.updated_at)}</TableCell>
              <TableCell data-label="错误" className="errorCell" title={job.error_message}>{compactCellError(job.error_message || '-')}</TableCell>
              <TableCell data-label="操作"><span className="muted">-</span></TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function JobCompactList({ jobs, selected, onSelect }: {
  jobs: Job[];
  selected?: string;
  onSelect: (j: Job) => void;
}) {
  if (jobs.length === 0) return <div className="empty compactEmpty">暂无工作流任务。</div>;

  return (
    <div className="jobCompactList">
      {jobs.map((job) => (
        <div
          key={job.job_id}
          className={`compactJobItem ${selected === job.job_id ? 'selected' : ''}`}
          role="button"
          tabIndex={0}
          onClick={() => onSelect(job)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              onSelect(job);
            }
          }}
        >
          <div className="compactJobHead">
            <div>
              <strong>{actionText(job.action)}</strong>
              <span className="mono">{short(job.job_id)} · {short(job.account_id || '-', 10)}</span>
            </div>
            <StatusBadge status={job.status} />
          </div>
          <div className="compactJobMeta">
            <span>{stepText(job.last_step)}</span>
            <span>{formatJobTime(job.updated_at)}</span>
          </div>
          {(job.error_message || canSubmitOtp(job)) && (
            <div className="compactJobFoot">
              <span title={job.error_message}>
                {canSubmitOtp(job) ? '需要 OTP' : compactCellError(job.error_message || '-')}
              </span>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

function MailboxPanel({ mailboxes, allMailboxes, selected, busy, showSecrets, oauthing, onSelect, onOAuth, onDelete, onDone, onError }: {
  mailboxes: Mailbox[];
  allMailboxes: Mailbox[];
  selected?: string;
  busy: boolean;
  showSecrets: boolean;
  oauthing: string;
  onSelect: (mailbox: Mailbox) => void;
  onOAuth: (emailAddress?: string) => Promise<void>;
  onDelete: (mailbox: Mailbox) => Promise<void>;
  onDone: (message: string) => void;
  onError: (message: string) => void;
}) {
  const [form, setForm] = useState({ email: '', password: '', refresh_token: '', access_token: '', status: 'AVAILABLE' });
  const [working, setWorking] = useState(false);
  const [showImport, setShowImport] = useState(false);

  function update(key: keyof typeof form, value: string) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  async function saveMailbox() {
    setWorking(true);
    try {
      const payload = { ...form, status: form.status || 'AVAILABLE' };
      const resp = await api<Mailbox>('/api/mailboxes', { method: 'POST', body: JSON.stringify(payload) });
      setForm({ email: '', password: '', refresh_token: '', access_token: '', status: 'AVAILABLE' });
      onDone(`邮箱已入池: ${resp.email_address}`);
    } catch (err) {
      onError(errorText(err));
    } finally {
      setWorking(false);
    }
  }

  return (
    <>
      <MailboxStatusStrip mailboxes={allMailboxes} />
      <div className="mailboxImportHeader">
        <div>
          <strong>主邮箱列表</strong>
          <span>{mailboxes.length === allMailboxes.length ? `${allMailboxes.length} 个主邮箱` : `显示 ${mailboxes.length} / ${allMailboxes.length} 个主邮箱`}</span>
        </div>
        <Button className="secondaryButton" onClick={() => setShowImport((value) => !value)}>
          {showImport ? <X size={15} /> : <Plus size={15} />}
          {showImport ? '收起导入' : '导入邮箱'}
        </Button>
      </div>
      {showImport && (
        <div className="mailboxForm">
          <Input placeholder="邮箱" value={form.email} onChange={(e) => update('email', e.target.value)} />
          <Input placeholder="邮箱密码，可空" type="password" value={form.password} onChange={(e) => update('password', e.target.value)} />
          <Input placeholder="Refresh token，可空" type="password" value={form.refresh_token} onChange={(e) => update('refresh_token', e.target.value)} />
          <Input placeholder="Access token，可空" type="password" value={form.access_token} onChange={(e) => update('access_token', e.target.value)} />
          <NativeSelect value={form.status} onChange={(e) => update('status', e.target.value)}>
            {mailboxUsageStatusOptions.map((s) => <NativeSelectOption key={s} value={s}>{statusText(s)}</NativeSelectOption>)}
          </NativeSelect>
          <Button onClick={saveMailbox} disabled={busy || working || !form.email.trim()}><Plus size={15} /> 入池</Button>
          <p className="hint">已有 Outlook 邮箱可直接导入；缺 Refresh Token 时可在列表中补 OAuth。</p>
        </div>
      )}
      <div className="tableWrap">
        <Table className="responsiveTable mailboxTable">
          <TableHeader>
            <TableRow><TableHead>主邮箱</TableHead><TableHead>最近邮件</TableHead><TableHead>占用</TableHead><TableHead>OAuth</TableHead><TableHead>Token</TableHead><TableHead>更新</TableHead><TableHead>错误</TableHead><TableHead>操作</TableHead></TableRow>
          </TableHeader>
          <TableBody>
            {mailboxes.length === 0 && <EmptyTableRow colSpan={8} text="暂无符合筛选条件的主邮箱。" />}
            {mailboxes.map((mailbox) => {
              const isOAuthing = oauthing === mailbox.email_address || oauthing === '*';
              const canOAuth = mailbox.is_primary && !!mailbox.password;
              const oauthLabel = authStatus(mailbox) === 'AUTHORIZED' ? '重新 OAuth' : '补 OAuth';
              return (
                <TableRow key={mailbox.email_address} className={selected === mailbox.email_address ? 'selected' : ''} onClick={() => onSelect(mailbox)}>
                  <TableCell data-label="主邮箱">
                    <div className="cellStack">
                      <span>{showSecrets ? mailbox.email_address : maskEmail(mailbox.email_address)}</span>
                      <small>{mailbox.primary_email || '-'}</small>
                    </div>
                  </TableCell>
                  <TableCell data-label="最近邮件"><MailboxActivityCell mailbox={mailbox} showSecrets={showSecrets} /></TableCell>
                  <TableCell data-label="占用"><StatusBadge status={mailbox.status} /></TableCell>
                  <TableCell data-label="OAuth"><StatusBadge status={authStatus(mailbox)} /></TableCell>
                  <TableCell data-label="Token"><TokenBadge mailbox={mailbox} /></TableCell>
                  <TableCell data-label="更新">{formatUnix(mailbox.updated_at)}</TableCell>
                  <TableCell data-label="错误" className="mailboxErrorCell" title={mailbox.last_error}>
                    <span>{compactCellError(mailbox.last_error || '-')}</span>
                  </TableCell>
                  <TableCell data-label="操作">
                    <div className="rowActions" onClick={(event) => event.stopPropagation()}>
                      {canOAuth ? (
                        <Button className="rowButtonText" {...buttonHint(isOAuthing ? 'OAuth 提交中' : `${oauthLabel}：${showSecrets ? mailbox.email_address : maskEmail(mailbox.email_address)}`)} disabled={busy || !!oauthing} onClick={() => onOAuth(mailbox.email_address)}>
                          <KeyRound size={14} /> {isOAuthing ? '提交中' : oauthLabel}
                        </Button>
                      ) : (
                        <span className="muted">-</span>
                      )}
                      <Button className="iconButton dangerButton" {...buttonHint('删除邮箱')} disabled={busy || !!oauthing} onClick={() => onDelete(mailbox)}>
                        <Trash2 size={14} />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
    </>
  );
}

function MailboxInboxSection({ mailbox, result, bans, showSecrets, loading, onFetch }: {
  mailbox: Mailbox;
  result?: InboxResult;
  bans: BanDetection[];
  showSecrets: boolean;
  loading: boolean;
  onFetch: (emailAddress?: string) => Promise<void>;
}) {
  const messages = result?.messages || [];
  return (
    <section className="drawerInbox">
      <div className="sectionTitle">
        <h3>收件箱</h3>
        <Button disabled={loading} onClick={() => onFetch(mailbox.email_address)}>
          <Inbox size={14} /> {loading ? '拉取中' : '拉取当前邮箱'}
        </Button>
      </div>
      {result?.error_message && <div className="inboxError">{compactToast(result.error_message)}</div>}
      {!!bans.length && <BanResults bans={bans} showSecrets={showSecrets} />}
      <div className="drawerInboxList">
        {messages.map((message, index) => (
          <article className="inboxMessage" key={`${message.mailbox_email}-${message.id || index}`}>
            <div className="inboxMessageHeader">
              <strong title={message.subject}>{message.subject || '-'}</strong>
              <span>{formatUnix(message.received_at_unix)}</span>
            </div>
            <div className="inboxMessageMeta">
              <span>发件人 {showSecrets ? (message.from_address || '-') : maskEmail(message.from_address)}</span>
              {message.otp && <em>OTP {showSecrets ? message.otp : mask(message.otp)}</em>}
            </div>
            <div className="recipientLine" title={formatEmailList(message.recipients, true)}>
              收件人 {formatEmailList(message.recipients, showSecrets)}
            </div>
            <p>{showSecrets ? (message.body_preview || '-') : maskPreview(message.body_preview || '-')}</p>
          </article>
        ))}
        {!result && <div className="inboxEmpty">点击“拉取当前邮箱”后显示当前邮箱的邮件。</div>}
        {result && !result.error_message && messages.length === 0 && <div className="inboxEmpty">当前邮箱没有新邮件。</div>}
      </div>
    </section>
  );
}

function LatestOtpLine({ mailbox, showSecrets }: {
  mailbox: Mailbox;
  showSecrets: boolean;
}) {
  if (!mailbox.latest_otp) return null;
  const value = showSecrets ? mailbox.latest_otp : mask(mailbox.latest_otp);
  const title = showSecrets ? (mailbox.latest_otp_subject || 'Latest OTP') : maskPreview(mailbox.latest_otp_subject || 'Latest OTP');
  return (
    <small className="latestOtp" title={title}>
      OTP {value} · {formatUnix(mailbox.latest_otp_received_at_unix)}
    </small>
  );
}

function MailboxActivityCell({ mailbox, showSecrets }: {
  mailbox: Mailbox;
  showSecrets: boolean;
}) {
  if (!mailbox.latest_otp) return <span className="muted">-</span>;
  const subject = showSecrets ? (mailbox.latest_otp_subject || '-') : maskPreview(mailbox.latest_otp_subject || '-');
  return (
    <div className="mailActivity">
      <LatestOtpLine mailbox={mailbox} showSecrets={showSecrets} />
      <small title={subject}>{subject}</small>
    </div>
  );
}

function BanResults({ bans, showSecrets }: {
  bans: BanDetection[];
  showSecrets: boolean;
}) {
  return (
    <div className="banStrip">
      {bans.map((ban, index) => (
        <div key={`${ban.email_address}-${ban.account_id}-${index}`}>
          <strong>{showSecrets ? ban.email_address : maskEmail(ban.email_address)}</strong>
          <span>{ban.account_updated ? '已标记 DEACTIVATED' : (ban.error_message || '未更新')}</span>
        </div>
      ))}
    </div>
  );
}

function RegistrationSummary({ job, runningCount }: { job?: Job; runningCount: number }) {
  const icon = runningCount > 0 ? <Clock size={16} /> : job?.status?.startsWith('FAILED') ? <AlertTriangle size={16} /> : <CheckCircle2 size={16} />;
  const title = runningCount > 0 ? `${runningCount} 个注册任务运行中` : job ? `最近一次：${statusText(job.status)}` : '暂无注册任务';
  const text = runningCount > 0
    ? '注册器同一时间只跑一个进程，重复触发会被后端锁拦截。'
    : job
      ? `${actionText(job.action)} · ${stepText(job.last_step)}${job.error_message ? ` · ${compactCellError(job.error_message)}` : ''}`
      : '点击“启动注册”后会创建 Outlook 邮箱并导入邮箱池。';

  return (
    <div className={`registrationSummary ${job?.status?.startsWith('FAILED') ? 'bad' : runningCount > 0 ? 'mid' : 'good'}`}>
      {icon}
      <div>
        <strong>{title}</strong>
        <span title={job?.error_message || text}>{text}</span>
      </div>
    </div>
  );
}

function MailboxStatusStrip({ mailboxes }: { mailboxes: Mailbox[] }) {
  const usageItems = ['AVAILABLE', 'ASSIGNED', 'REGISTERED', 'USER_ALREADY_EXISTS', 'REGISTRATION_FAILED', 'BLOCKED'];
  const authItems = ['AUTHORIZED', 'OAUTH_PENDING', 'AUTH_FAILED', 'NEEDS_MANUAL_VERIFICATION'];
  return (
    <div className="mailboxStatusStrip" aria-label="邮箱状态汇总">
      <div className="statusStripGroup">
        <h4>占用状态</h4>
        <div className="statusStripGrid">
          {usageItems.map((status) => (
            <div key={status}>
              <strong>{mailboxes.filter((mailbox) => mailbox.status === status).length}</strong>
              <span>{statusText(status)}</span>
            </div>
          ))}
        </div>
      </div>
      <div className="statusStripGroup">
        <h4>OAuth 状态</h4>
        <div className="statusStripGrid">
          {authItems.map((status) => (
            <div key={status}>
              <strong>{mailboxes.filter((mailbox) => authStatus(mailbox) === status).length}</strong>
              <span>{statusText(status)}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function MailboxAliasesSection({ aliases, showSecrets, onDelete }: {
  aliases: Mailbox[];
  showSecrets: boolean;
  onDelete: (mailbox: Mailbox) => Promise<void>;
}) {
  return (
    <section className="aliasSection">
      <div className="sectionTitle">
        <h3>Alias</h3>
        <span className="muted">{aliases.length}</span>
      </div>
      <div className="aliasList">
        {aliases.map((alias) => (
          <div className="aliasItem" key={alias.email_address}>
            <div className="aliasIdentity">
              <strong>{showSecrets ? alias.email_address : maskEmail(alias.email_address)}</strong>
              <span><StatusBadge status={alias.status} /> <StatusBadge status={authStatus(alias)} /></span>
            </div>
            <MailboxActivityCell mailbox={alias} showSecrets={showSecrets} />
            <Button className="iconButton dangerButton" {...buttonHint('删除 Alias')} onClick={() => onDelete(alias)}>
              <Trash2 size={14} />
            </Button>
          </div>
        ))}
        {aliases.length === 0 && <div className="inboxEmpty">暂无 Alias 邮箱。</div>}
      </div>
    </section>
  );
}

function MailboxDetails({ mailbox, showSecrets, inboxResult, bans, aliases, inboxLoading, onCopy, onFetchInbox, onDelete }: {
  mailbox: Mailbox;
  showSecrets: boolean;
  inboxResult?: InboxResult;
  bans: BanDetection[];
  aliases: Mailbox[];
  inboxLoading: boolean;
  onCopy: (label: string, value: string) => void;
  onFetchInbox: (emailAddress?: string) => Promise<void>;
  onDelete: (mailbox: Mailbox) => Promise<void>;
}) {
  const [activeTab, setActiveTab] = useState<MailboxDetailTab>('overview');
  const inboxMessageCount = inboxResult?.messages?.length || 0;

  useEffect(() => {
    setActiveTab('overview');
  }, [mailbox.email_address]);

  return (
    <div className="details mailboxDetailView">
      <nav className="mailboxDetailTabs" aria-label="邮箱详情">
        <Button className={activeTab === 'overview' ? 'active' : ''} onClick={() => setActiveTab('overview')}>概览</Button>
        <Button className={activeTab === 'aliases' ? 'active' : ''} onClick={() => setActiveTab('aliases')}>Alias <span>{aliases.length}</span></Button>
        <Button className={activeTab === 'inbox' ? 'active' : ''} onClick={() => setActiveTab('inbox')}>收件箱 <span>{inboxMessageCount}</span></Button>
      </nav>

      {activeTab === 'overview' && (
        <section className="mailboxTabPanel">
          <div className="mailboxSummary">
            <div className="mailboxSummaryHead">
              <div>
                <span>{mailbox.is_primary ? '主邮箱' : 'Alias'}</span>
                <strong>{showSecrets ? mailbox.email_address : maskEmail(mailbox.email_address)}</strong>
              </div>
              <div className="summaryBadges">
                <StatusBadge status={mailbox.status} />
                <StatusBadge status={authStatus(mailbox)} />
              </div>
            </div>
            <div className="latestOtpPanel">
              <span>最近 OTP</span>
              <strong className="mono">{showSecrets ? (mailbox.latest_otp || '-') : mask(mailbox.latest_otp)}</strong>
              <em>{formatUnix(mailbox.latest_otp_received_at_unix)}</em>
            </div>
          </div>
          <h3>邮箱</h3>
          <KV label="邮箱" value={showSecrets ? mailbox.email_address : maskEmail(mailbox.email_address)} copyValue={mailbox.email_address} copyDisabled={!mailbox.email_address} masked={!showSecrets} onCopy={onCopy} />
          <KV label="密码" value={showSecrets ? mailbox.password : mask(mailbox.password)} copyValue={mailbox.password} copyDisabled={!mailbox.password} masked={!showSecrets} mono onCopy={onCopy} />
          <KV label="占用" value={statusText(mailbox.status)} copyValue={mailbox.status || '-'} onCopy={onCopy} />
          <KV label="OAuth" value={statusText(authStatus(mailbox))} onCopy={onCopy} />
          <KV label="Token" value={tokenText(mailbox)} onCopy={onCopy} />
          <KV label="Alias 数" value={String(aliases.length)} onCopy={onCopy} />
          <KV label="主邮箱" value={showSecrets ? (mailbox.primary_email || '-') : maskEmail(mailbox.primary_email)} copyValue={mailbox.primary_email || '-'} copyDisabled={!mailbox.primary_email} masked={!showSecrets} onCopy={onCopy} />
          <KV label="Refresh" value={showSecrets ? mailbox.refresh_token : mask(mailbox.refresh_token)} copyValue={mailbox.refresh_token} copyDisabled={!mailbox.refresh_token} masked={!showSecrets} mono onCopy={onCopy} />
          <KV label="Access" value={showSecrets ? mailbox.access_token : mask(mailbox.access_token)} copyValue={mailbox.access_token} copyDisabled={!mailbox.access_token} masked={!showSecrets} mono onCopy={onCopy} />
          <KV label="最近 OTP" value={showSecrets ? mailbox.latest_otp : mask(mailbox.latest_otp)} copyValue={mailbox.latest_otp} copyDisabled={!mailbox.latest_otp} masked={!showSecrets} mono onCopy={onCopy} />
          <KV label="OTP 时间" value={formatUnix(mailbox.latest_otp_received_at_unix)} onCopy={onCopy} />
          <KV label="创建时间" value={formatUnix(mailbox.created_at)} onCopy={onCopy} />
          <KV label="更新时间" value={formatUnix(mailbox.updated_at)} onCopy={onCopy} />
          <KV label="错误" value={mailbox.last_error || '-'} onCopy={onCopy} />
          <div className="buttonRow detailActions">
            <Button className="dangerButton" onClick={() => onDelete(mailbox)}>
              <Trash2 size={14} /> {mailbox.is_primary ? '删除主邮箱' : '删除 Alias'}
            </Button>
          </div>
        </section>
      )}

      {activeTab === 'aliases' && (
        <div className="mailboxTabPanel">
          <MailboxAliasesSection aliases={aliases} showSecrets={showSecrets} onDelete={onDelete} />
        </div>
      )}

      {activeTab === 'inbox' && (
        <div className="mailboxTabPanel">
          <MailboxInboxSection
            mailbox={mailbox}
            result={inboxResult}
            bans={bans}
            showSecrets={showSecrets}
            loading={inboxLoading}
            onFetch={onFetchInbox}
          />
        </div>
      )}
    </div>
  );
}

function CreateAccountForm({ onDone, onError }: {
  onDone: (message: string) => void;
  onError: (message: string) => void;
}) {
  const [form, setForm] = useState({
    email: '',
    password: ''
  });
  const [working, setWorking] = useState('');

  function update(key: keyof typeof form, value: string) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  async function run(label: string, path: string, payload: unknown) {
    setWorking(label);
    try {
      const resp = await api<any>(path, { method: 'POST', body: JSON.stringify(payload) });
      if (resp.error_message) {
        onError(resp.error_message);
      } else {
        onDone(`${label} 已提交: ${resp.job_id || resp.account_id || 'ok'}`);
      }
    } catch (err) {
      onError(errorText(err));
    } finally {
      setWorking('');
    }
  }

  return (
    <div className="createAccount">
      <div className="workflowButtons">
        <Button className="primaryButton" onClick={() => run('注册账号', '/api/workflows/register', {})} disabled={!!working}>
          <Play size={15} /> 注册账号
        </Button>
        <Button className="secondaryButton" onClick={() => run('注册并激活', '/api/workflows/register-and-activate', {})} disabled={!!working}>
          <ShieldCheck size={15} /> 注册并激活
        </Button>
      </div>
      <div className="formGrid">
        <Input placeholder="邮箱，可空" value={form.email} onChange={(e) => update('email', e.target.value)} />
        <Input placeholder="密码，可空" type="password" value={form.password} onChange={(e) => update('password', e.target.value)} />
      </div>
      <div className="buttonRow">
        <Button onClick={() => run('创建账号', '/api/accounts', form)} disabled={!!working}><Plus size={15} /> 创建账号</Button>
      </div>
      <p className="hint">{working ? `正在执行：${working}` : '邮箱或密码留空时会由后端自动分配。'}</p>
    </div>
  );
}

function TokenEditor({ label, field, account, showSecrets, onCopy, onSave }: {
  label: string;
  field: 'session_token' | 'access_token';
  account: Account;
  showSecrets: boolean;
  onCopy: (label: string, value: string) => void;
  onSave: (account: Account, token: string) => Promise<void>;
}) {
  const current = account[field] || '';
  const [value, setValue] = useState(current);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setValue(account[field] || '');
  }, [account.account_id, account.session_token, account.access_token, field]);

  async function save() {
    setSaving(true);
    try {
      await onSave(account, value.trim());
    } finally {
      setSaving(false);
    }
  }

  function copyFromInput(event: React.ClipboardEvent<HTMLInputElement>) {
    if (!value.trim()) return;
    event.preventDefault();
    event.clipboardData.setData('text/plain', value);
  }

  return (
    <div className="editLine">
      <span>{label}</span>
      <Input
        className="mono"
        type={showSecrets ? 'text' : 'password'}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        onCopy={copyFromInput}
        placeholder={`${label.toLowerCase()} token`}
      />
      <Button
        className="copyButton"
        {...buttonHint(`复制 ${label}`)}
        disabled={!value.trim()}
        onClick={() => onCopy(label, value)}
      >
        <Copy size={14} />
      </Button>
      <Button {...buttonHint(`保存 ${label}`)} onClick={save} disabled={saving || value.trim() === current}>
        <Save size={14} /> 保存
      </Button>
    </div>
  );
}

function KV({ label, value, mono, copyValue, copyDisabled, copyHint, masked, onCopy }: {
  label: string;
  value: string;
  mono?: boolean;
  copyValue?: string;
  copyDisabled?: boolean;
  copyHint?: string;
  masked?: boolean;
  onCopy?: (label: string, value: string) => void;
}) {
  const actualValue = copyValue ?? value;
  const inputValue = masked ? actualValue : value;
  const disabled = copyDisabled || !actualValue || actualValue === '-';
  const hint = disabled && copyHint ? copyHint : `复制 ${label}`;
  const copy = () => {
    if (onCopy) {
      onCopy(label, actualValue);
      return;
    }
    void copyText(actualValue);
  };
  const copyFromInput = (event: React.ClipboardEvent<HTMLInputElement>) => {
    if (disabled) return;
    event.preventDefault();
    event.clipboardData.setData('text/plain', actualValue);
  };
  return (
    <div className="kv">
      <span>{label}</span>
      <input
        className={[mono ? 'mono valueButton' : 'valueButton', masked ? 'maskedValue' : ''].filter(Boolean).join(' ')}
        readOnly
        aria-label={`${label}值`}
        title={value || '-'}
        value={inputValue || '-'}
        onFocus={(event) => event.currentTarget.select()}
        onCopy={copyFromInput}
      />
      <Button className="copyButton" {...buttonHint(hint)} disabled={disabled} onClick={copy}>
        <Copy size={14} />
      </Button>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const cls = status.includes('FAILED') || status.includes('EXISTS') || status === 'BLOCKED' || status === 'NEEDS_MANUAL_VERIFICATION' ? 'bad' : status === 'SUCCEEDED' || status === 'ACTIVATED' || status === 'REGISTERED' || status === 'AUTHORIZED' ? 'good' : 'mid';
  const label = statusText(status);
  const variant = cls === 'bad' ? 'destructive' : cls === 'good' ? 'default' : 'secondary';
  return <Badge className={`badge ${cls}`} variant={variant} title={status || '-'}>{label}</Badge>;
}

function statusText(status: string) {
  return accountStatusLabels[status] || jobStatusLabels[status] || mailboxStatusLabels[status] || status || '-';
}

function PlusBadge({ account }: { account: Account }) {
  if (account.plus_active) return <Badge className="badge good">Plus</Badge>;
  return <TrialBadge eligible={account.plus_trial_eligible} />;
}

function TierBadge({ tier }: { tier: string }) {
  const value = tierText(tier);
  const normalized = String(tier || '').trim().toLowerCase();
  const cls = normalized === 'free' ? 'mid' : normalized ? 'good' : 'mid';
  return <Badge className={`badge ${cls}`} variant={cls === 'good' ? 'default' : 'secondary'}>{value}</Badge>;
}

function TrialBadge({ eligible }: { eligible?: boolean }) {
  if (eligible === true) return <Badge className="badge good">0元</Badge>;
  if (eligible === false) return <Badge className="badge bad" variant="destructive">非0元</Badge>;
  return <Badge className="badge mid" variant="secondary">未知</Badge>;
}

function TokenBadge({ mailbox }: { mailbox: Mailbox }) {
  const value = tokenText(mailbox);
  if (mailbox.refresh_token && authStatus(mailbox) === 'AUTHORIZED') return <Badge className="badge good">{value}</Badge>;
  if (mailbox.refresh_token || mailbox.access_token) return <Badge className="badge mid" variant="secondary">{value}</Badge>;
  return <Badge className="badge bad" variant="destructive">{value}</Badge>;
}

function tokenText(mailbox: Mailbox) {
  if (mailbox.refresh_token && authStatus(mailbox) === 'AUTHORIZED') return 'Refresh 可用';
  if (mailbox.refresh_token) return 'Refresh 待验证';
  if (mailbox.access_token) return '仅 Access';
  return '缺 Token';
}

function actionText(action: string) {
  return actionLabels[action] || action || '-';
}

function stepText(step: string) {
  return stepLabels[step] || step || '-';
}

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, { headers: { 'Content-Type': 'application/json' }, ...init });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || resp.statusText);
  return data as T;
}

function canRegister(account: Account) {
  return !isUserAlreadyExistsAccount(account) && !hasRegisteredSession(account);
}

function canActivate(account: Account) {
  return !isUserAlreadyExistsAccount(account) &&
    account.status !== 'ACTIVATED' &&
    !account.plus_active &&
    normalizeTier(account.tier) === 'free' &&
    account.plus_trial_eligible === true &&
    (!!account.session_token || !!account.access_token);
}

function canAutopay(account: Account) {
  const tier = normalizeTier(account.tier);
  return !isUserAlreadyExistsAccount(account) &&
    account.status !== 'ACTIVATED' &&
    !account.plus_active &&
    account.plus_trial_eligible !== false &&
    (tier === '' || tier === 'free') &&
    (!!account.session_token || !!account.access_token);
}

function canProbeAccount(account: Account) {
  return !isUserAlreadyExistsAccount(account) && !!account.session_token;
}

function canCreateCheckoutLink(account: Account) {
  return !isUserAlreadyExistsAccount(account) &&
    account.status !== 'ACTIVATED' &&
    !account.plus_active &&
    (!!account.session_token || !!account.access_token);
}

function probeAccountHint(account: Account) {
  if (normalizeTier(account.tier) === 'plus' || account.plus_active) {
    return '已是 Plus，直接探测 Tier';
  }
  if (account.plus_trial_eligible !== undefined && account.plus_trial_eligible !== null) {
    return '资格已探测，直接探测 Tier';
  }
  return '先探测 Plus 资格，再探测 Tier';
}

function canRefreshAccessToken(account: Account) {
  return !isUserAlreadyExistsAccount(account) && !!account.session_token && !account.access_token;
}

function canLoginSession(account: Account) {
  return !isUserAlreadyExistsAccount(account) && !!account.email && !!account.password;
}

function loginActionLabel(account: Account) {
  if (!account.session_token) return '登录获取 Session';
  if (!account.access_token) return '登录刷新 Access Token';
  return '登录刷新 Token';
}

function loginActionHint(account: Account) {
  if (!account.session_token) return '通过账号密码登录并获取 Session Token';
  if (!account.access_token) return '重新登录并刷新 Access Token';
  return '重新登录并刷新 Session / Access Token';
}

function buttonHint(label: string) {
  return { title: label, 'aria-label': label, 'data-tooltip': label };
}

function hasRegisteredSession(account: Account) {
  return account.status === 'REGISTERED' || account.status === 'ACTIVATED' || !!account.session_token || !!account.access_token;
}

function isUserAlreadyExistsAccount(account: Account) {
  return account.status === 'USER_ALREADY_EXISTS' || account.status === 'EMAIL_ALREADY_EXISTS';
}

function canSubmitOtp(job: Job) {
  return job.status === 'RUNNING' && (job.action === 'REGISTER' || job.action === 'LOGIN_SESSION' || job.action === 'ACTIVATE' || job.action === 'AUTOPAY' || job.action === 'GOPAY_APP' || job.action === 'REGISTER_AND_ACTIVATE');
}

function stepResultData(job: Job, stepName: string): any | null {
  const step = (job.steps || []).find((item) => item.step_name === stepName);
  if (!step?.result_json) return null;
  try {
    return JSON.parse(step.result_json);
  } catch {
    return null;
  }
}

function otpSubmitLabel(job: Job) {
  if (job.action === 'LOGIN_SESSION') return '登录 OTP';
  if (job.action === 'GOPAY_APP') return 'GoPay OTP';
  if (job.action === 'ACTIVATE' || job.action === 'AUTOPAY' || (job.action === 'REGISTER_AND_ACTIVATE' && (job.last_step === 'gopay_login' || job.last_step === 'gopay_payment'))) {
    return '支付 OTP';
  }
  return '注册 OTP';
}

function short(value: string, size = 8) {
  if (!value) return '-';
  return value.length > size ? `${value.slice(0, size)}…` : value;
}

function mask(value: string) {
  return value ? '••••••••' : '-';
}

function maskEmail(value: string) {
  if (!value) return '-';
  const [local, domain] = value.split('@');
  if (!local || !domain) return mask(value);
  return `${local.slice(0, 2)}***@${domain}`;
}

function formatEmailList(values: string[] | undefined, showSecrets: boolean) {
  const list = values || [];
  if (list.length === 0) return '-';
  return list.map((value) => showSecrets ? value : maskEmail(value)).join(', ');
}

function maskPreview(value: string) {
  return String(value || '-').replace(/\b\d{6}\b/g, '••••••');
}

function inboxResultForMailbox(response: InboxResponse | null, email: string) {
  const target = normalizeUiEmail(email);
  if (!response || !target) return undefined;
  return (response.results || []).find((result) => {
    if (normalizeUiEmail(result.mailbox?.email_address || '') === target) return true;
    return (result.messages || []).some((message) => (
      normalizeUiEmail(message.mailbox_email) === target ||
      (message.recipients || []).some((recipient) => normalizeUiEmail(recipient) === target)
    ));
  });
}

function latestOtpForEmail(response: InboxResponse | null, mailboxes: Mailbox[], email: string): LatestOtp | null {
  const target = normalizeUiEmail(email);
  if (!target) return null;
  const candidates: LatestOtp[] = [];
  const mailbox = mailboxes.find((item) => normalizeUiEmail(item.email_address) === target);
  if (mailbox?.latest_otp) {
    candidates.push({
      otp: mailbox.latest_otp,
      subject: mailbox.latest_otp_subject,
      received_at_unix: mailbox.latest_otp_received_at_unix
    });
  }
  const result = inboxResultForMailbox(response, email);
  for (const message of result?.messages || []) {
    const matchesTarget = normalizeUiEmail(message.mailbox_email) === target ||
      (message.recipients || []).some((recipient) => normalizeUiEmail(recipient) === target);
    if (!matchesTarget || !message.otp) continue;
    candidates.push({
      otp: message.otp,
      subject: message.subject,
      received_at_unix: message.received_at_unix
    });
  }
  if (result?.mailbox?.latest_otp && normalizeUiEmail(result.mailbox.email_address) === target) {
    candidates.push({
      otp: result.mailbox.latest_otp,
      subject: result.mailbox.latest_otp_subject,
      received_at_unix: result.mailbox.latest_otp_received_at_unix
    });
  }
  candidates.sort((a, b) => b.received_at_unix - a.received_at_unix);
  return candidates[0] || null;
}

function mailboxContextForEmail(mailboxes: Mailbox[], email: string): AccountMailboxContext {
  const accountEmail = normalizeUiEmail(email);
  const mailbox = mailboxes.find((item) => normalizeUiEmail(item.email_address) === accountEmail);
  const primaryEmail = normalizeUiEmail(mailbox?.primary_email || canonicalUiEmail(accountEmail));
  return {
    account_email: accountEmail,
    primary_email: primaryEmail,
    is_split: !!accountEmail && !!primaryEmail && accountEmail !== primaryEmail,
    known: !!mailbox
  };
}

function accountInboxHint(email: string, context: AccountMailboxContext | null, showSecrets: boolean) {
  const accountEmail = showSecrets ? email : maskEmail(email);
  if (context?.is_split) {
    const primaryEmail = showSecrets ? context.primary_email : maskEmail(context.primary_email);
    return `用主邮箱 ${primaryEmail} 拉取收件箱，按分裂邮箱 ${accountEmail} 匹配 OTP`;
  }
  return `拉取当前账号邮箱 ${accountEmail} 的最新 OTP`;
}

function bansForMailbox(response: InboxResponse | null, email: string) {
  const target = normalizeUiEmail(email);
  if (!response || !target) return [];
  return (response.bans || []).filter((ban) => (
    normalizeUiEmail(ban.mailbox_email) === target ||
    normalizeUiEmail(ban.email_address) === target
  ));
}

function aliasesForMailbox(mailboxes: Mailbox[], mailbox: Mailbox) {
  const primary = normalizeUiEmail(mailbox.is_primary ? mailbox.email_address : mailbox.primary_email);
  if (!primary) return [];
  return mailboxes
    .filter((item) => !item.is_primary && normalizeUiEmail(item.primary_email) === primary)
    .sort((a, b) => b.updated_at - a.updated_at);
}

function mailboxMatchesFilter(mailbox: Mailbox, allMailboxes: Mailbox[], filter: string) {
  if (!filter) return true;
  const aliases = aliasesForMailbox(allMailboxes, mailbox);
  if (isAuthFilter(filter)) {
    return authStatus(mailbox) === filter || aliases.some((alias) => authStatus(alias) === filter);
  }
  return mailbox.status === filter || aliases.some((alias) => alias.status === filter);
}

function isAuthFilter(value: string) {
  return value === 'AUTHORIZED' || value === 'OAUTH_PENDING' || value === 'AUTH_FAILED' || value === 'NEEDS_MANUAL_VERIFICATION';
}

function authStatus(mailbox: Mailbox) {
  const value = String(mailbox.auth_status || '').trim();
  if (value) return value;
  if (mailbox.refresh_token) return 'AUTHORIZED';
  return 'OAUTH_PENDING';
}

function normalizeUiEmail(value: string) {
  return String(value || '').trim().toLowerCase();
}

function canonicalUiEmail(value: string) {
  const normalized = normalizeUiEmail(value);
  const [local, domain] = normalized.split('@');
  if (!local || !domain) return normalized;
  return `${local.split('+')[0]}@${domain}`;
}

function formatUnix(value: number) {
  return value ? new Date(value * 1000).toLocaleString() : '-';
}

function formatJobTime(value: string | number) {
  if (!value) return '-';
  if (typeof value === 'number') return formatUnix(value);
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function stepDuration(step: Step) {
	if (!step.started_at) return null;
	const end = step.completed_at || Math.floor(Date.now() / 1000);
	const seconds = Math.max(0, end - step.started_at);
	if (seconds < 1) return <small className="stepTime">刚刚</small>;
	if (seconds < 60) return <small className="stepTime">{seconds}s</small>;
	return <small className="stepTime">{Math.floor(seconds / 60)}m {seconds % 60}s</small>;
}

function stepProgressText(step: Step, workflowProgress?: WorkflowProgress | null) {
  const data = parseJSON(step.result_json);
  if (data && typeof data === 'object') {
    const record = data as Record<string, any>;
    const progress = record.progress && typeof record.progress === 'object' ? record.progress as Record<string, any> : {};
    const message = stringValue(record.progress_message) || stringValue(progress.message);
    if (message) {
      const atUnix = numberValue(record.progress_at_unix) || numberValue(progress.at_unix);
      return atUnix ? `${message} · ${formatUnix(atUnix)}` : message;
    }
  }
  if (!workflowProgress || workflowProgress.step_name !== step.step_name) return '';
  const message = workflowProgress.error_message || statusText(workflowProgress.status.toUpperCase());
  if (!message) return '';
  return workflowProgress.updated_at_unix ? `${message} · ${formatUnix(workflowProgress.updated_at_unix)}` : message;
}

function trialText(value?: boolean) {
  if (value === true) return '0元试用';
  if (value === false) return '非0元';
  return '未知';
}

function plusText(account: Account) {
  if (account.plus_active) return 'Plus 已开通';
  return trialText(account.plus_trial_eligible);
}

function tierText(tier: string) {
  return normalizeTier(tier) || '未知';
}

function normalizeTier(tier: string) {
  return String(tier || '').trim().toLowerCase();
}

function errorText(err: unknown) {
  return err instanceof Error ? err.message : String(err);
}

function compactToast(value: string) {
  const text = String(value || '');
  return text.length > 150 ? `${text.slice(0, 150)}...` : text;
}

function compactCellError(value: string) {
  const text = String(value || '-');
  return text.length > 24 ? `${text.slice(0, 24)}...` : text;
}

function formatJSON(value: string) {
	try {
		return JSON.stringify(JSON.parse(value), null, 2);
	} catch {
		return value;
	}
}

function parseJSON(value: string): unknown {
  if (!value) return null;
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}

function stringValue(value: unknown) {
  return typeof value === 'string' ? value : '';
}

function numberValue(value: unknown) {
  if (typeof value === 'number') return value;
  if (typeof value === 'string') {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : 0;
  }
  return 0;
}

async function copyText(value: string): Promise<boolean> {
  if (!value) return false;
  if (!window.isSecureContext || !navigator.clipboard?.writeText) {
    return copyTextFallback(value);
  }
  try {
    await navigator.clipboard.writeText(value);
    return true;
  } catch {
    return copyTextFallback(value);
  }
}

function copyTextFallback(value: string): boolean {
  try {
    const textarea = document.createElement('textarea');
    textarea.value = value;
    textarea.setAttribute('readonly', 'true');
    textarea.style.position = 'fixed';
    textarea.style.top = '0';
    textarea.style.left = '0';
    textarea.style.width = '1px';
    textarea.style.height = '1px';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.focus({ preventScroll: true });
    textarea.select();
    textarea.setSelectionRange(0, textarea.value.length);
    const copied = document.execCommand('copy');
    document.body.removeChild(textarea);
    return copied;
  } catch {
    return false;
  }
}

createRoot(document.getElementById('root')!).render(
  <TooltipProvider>
    <App />
  </TooltipProvider>
);
