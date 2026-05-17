import React, { useCallback, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Clock,
  Copy,
  Eye,
  EyeOff,
  Inbox,
  KeyRound,
  ListChecks,
  Mail,
  Play,
  Plus,
  QrCode,
  RefreshCcw,
  Save,
  Search,
  ShieldCheck,
  Trash2,
  WalletCards,
  X,
  Zap
} from 'lucide-react';
import QRCode from 'qrcode';
import { Dialog as DialogPrimitive } from 'radix-ui';
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { TooltipProvider } from '@/components/ui/tooltip';
import type { GoPayUserStatusResponse } from './proto/orchestrator_gopay_app';
import type { Job, JobEvent, JobSnapshot, JobStep as Step, WorkflowProgress } from './proto/orchestrator_job';
import './styles.css';

type Account = {
  account_id: string;
  email: string;
  password: string;
  status: string;
  error_message: string;
  activation_channel?: string;
  session_token: string;
  access_token: string;
  plus_trial_eligible?: boolean;
  plus_active?: boolean;
  tier: string;
  created_at: number;
  updated_at: number;
};

type ManualAddBalanceConfirmResponse = {
  success: boolean;
  job_id: string;
  error_message?: string;
};

function isRunningSnapshot(snapshot: JobSnapshot) {
  return snapshot.job?.status === 'RUNNING';
}

function jobSnapshotMatchesStatus(snapshot: JobSnapshot, status: string) {
  return !status || snapshot.job?.status === status;
}

function mergeJobSnapshots(prev: JobSnapshot[], snapshot: JobSnapshot, include: boolean) {
  const jobID = snapshot.job?.job_id;
  if (!jobID) return prev;
  const index = prev.findIndex((item) => item.job?.job_id === jobID);
  if (!include) {
    return index === -1 ? prev : prev.filter((item) => item.job?.job_id !== jobID);
  }
  if (index === -1) return [snapshot, ...prev];
  const next = [...prev];
  next[index] = snapshot;
  return next;
}

function mergeJobEvents(prev: JobEvent[], event: JobEvent, jobID: string) {
  if (!event?.event_id || event.job_id !== jobID) return prev;
  const next = prev.filter((item) => item.event_id !== event.event_id);
  return [event, ...next].sort((a, b) => b.event_id - a.event_id).slice(0, 80);
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

type GPTEmailAllocation = {
  email: string;
  primary_email: string;
  is_primary: boolean;
  status: string;
  splittable: boolean;
  assigned_account_id: string;
  last_error: string;
  created_at: number;
  updated_at: number;
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

type GoPayDashboardStateResponse = GoPayUserStatusResponse & {
  user_id: string;
  wa_phone: string;
  wa_phone_error_message?: string;
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

type GoPayOTPChannel = '' | 'sms' | 'wa';
type ConcreteGoPayPaymentChannel = Exclude<GoPayOTPChannel, ''>;
const GO_PAY_PAYMENT_CHANNELS: ConcreteGoPayPaymentChannel[] = ['sms', 'wa'];
type GoPayAddBalanceMethod = '' | 'manual_transfer' | 'envelope' | 'rekberinaja';
type ConcreteGoPayAddBalanceMethod = Exclude<GoPayAddBalanceMethod, ''>;
const GO_PAY_ADD_BALANCE_METHODS: ConcreteGoPayAddBalanceMethod[] = ['manual_transfer', 'envelope', 'rekberinaja'];

type Toast = { kind: 'ok' | 'error'; text: string } | null;
type ViewKey = 'accounts' | 'gopay' | 'mailboxes' | 'jobs';
type WorkflowTab = 'all' | 'gpt' | 'gopay' | 'mailbox';
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
  GOPAY_PAYMENT: 'GoPay 支付',
  GOPAY_PAYMENT_REBIND: 'GoPay 支付换绑',
  REGISTER_AND_ACTIVATE: '注册并激活',
  PROBE_ACCOUNT: '探测账号',
  REGISTER_MAILBOX: '注册 Outlook 邮箱',
  MAILBOX_OAUTH: 'Microsoft OAuth'
};

const gptWorkflowActions = new Set(['REGISTER', 'LOGIN_SESSION', 'ACTIVATE', 'AUTOPAY', 'REGISTER_AND_ACTIVATE', 'PROBE_ACCOUNT']);
const gopayWorkflowActions = new Set(['GOPAY_APP', 'GOPAY_PAYMENT', 'GOPAY_PAYMENT_REBIND']);
const mailboxWorkflowActions = new Set(['REGISTER_MAILBOX', 'MAILBOX_OAUTH']);

const stepLabels: DisplayLabelMap = {
  register_account: '注册账号',
  login_session: '登录取 Token',
  ensure_logon: '确认登录',
  create_email: '创建邮箱',
  wait_outlook_otp: '等待 Outlook OTP',
  gopay_app_login: 'GoPay 登录',
  gopay_app_ensure_token_available: 'EnsureTokenAvailable',
  gopay_app_ensure_pin_settled: 'EnsurePinSettled',
  gopay_app_change_phone: 'GoPay 换绑',
  gopay_app_change_phone_start: '开始换绑',
  gopay_app_change_phone_sms_wait: '等待换绑短信',
  gopay_app_change_phone_retry: '重发换绑短信',
  gopay_app_change_phone_cancel: '取消换绑号码',
  gopay_app_change_phone_complete: '完成换绑',
  gopay_app_signup_phone: '获取未注册 GoPay 号',
  gopay_app_wa_phone_check: '检测 WA GoPay 号',
  gopay_app_deactivate: 'GoPay 注销',
  gopay_app_deactivate_start: '开始注销',
  gopay_app_deactivate_sms_wait: '等待注销短信',
  gopay_app_deactivate_sms_finish: '结束注销号码',
  gopay_app_deactivate_complete: '完成注销',
  gopay_app_signup: 'GoPay 注册',
  gopay_app_create_pin: 'EnsurePinSettled',
  gopay_app_add_balance: 'GoPay 加余额',
  gopay_app_add_balance_confirm: '等待转账确认',
  gopay_app_sms_finish: '结束 GoPay 接码',
  gopay_app_sms_request_more: '追加短信接码',
  gopay_login: 'GoPay 登录',
  gopay_payment_prepare: '准备 GoPay 支付',
  gopay_payment: 'GoPay 支付',
  gopay_payment_rebind: 'GoPay 支付换绑',
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
  const [gptEmailAllocations, setGptEmailAllocations] = useState<GPTEmailAllocation[]>([]);
  const [activeView, setActiveView] = useState<ViewKey>('accounts');
  const [workflowTab, setWorkflowTab] = useState<WorkflowTab>('all');
  const [selectedAccount, setSelectedAccount] = useState<Account | null>(null);
  const [selectedJobSnapshot, setSelectedJobSnapshot] = useState<JobSnapshot | null>(null);
  const [selectedJobEvents, setSelectedJobEvents] = useState<JobEvent[]>([]);
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
  const [goPayStateStatus, setGoPayStateStatus] = useState<GoPayDashboardStateResponse | null>(null);
  const [goPayStateLoading, setGoPayStateLoading] = useState(false);
  const [refreshingAccessTokenIds, setRefreshingAccessTokenIds] = useState<Set<string>>(new Set());
  const [loadError, setLoadError] = useState('');
  const [nowUnix, setNowUnix] = useState(() => Math.floor(Date.now() / 1000));
  const jobs = jobSnapshots.map((snapshot) => snapshot.job).filter((job): job is Job => !!job);
  const runningJobs = runningJobSnapshots.map((snapshot) => snapshot.job).filter((job): job is Job => !!job);
  const runningJobCount = runningJobs.length;
  const runningAccountIds = new Set(runningJobs.filter((job) => job.account_id).map((job) => job.account_id));
  const runningJobByAccountID = latestJobMap(runningJobs.filter((job) => job.account_id), (job) => job.account_id);
  const runningMailboxJobByEmail = latestJobMap(
    runningJobs.filter((job) => mailboxWorkflowEmail(job)),
    (job) => mailboxWorkflowEmail(job)
  );
  const selectedJob = selectedJobSnapshot?.job || null;
  const selectedJobProgress = selectedJobSnapshot?.progress || null;
  const selectedJobID = selectedJob?.job_id || '';
  const runningJobIDsKey = runningJobs.map((job) => job.job_id).sort().join('|');

  const applyJobSnapshot = useCallback((snapshot: JobSnapshot) => {
    if (!snapshot?.job?.job_id) return;
    setJobSnapshots((prev) => mergeJobSnapshots(prev, snapshot, jobSnapshotMatchesStatus(snapshot, jobStatus)));
    setRunningJobSnapshots((prev) => mergeJobSnapshots(prev, snapshot, isRunningSnapshot(snapshot)));
    setSelectedJobSnapshot((prev) => prev?.job?.job_id === snapshot.job?.job_id ? snapshot : prev);
  }, [jobStatus]);

  const applyJobEvent = useCallback((jobEvent: JobEvent) => {
    if (!jobEvent?.job_id) return;
    if (jobEvent.snapshot) {
      applyJobSnapshot(jobEvent.snapshot);
    }
    if (selectedJobID && jobEvent.job_id === selectedJobID) {
      setSelectedJobEvents((prev) => mergeJobEvents(prev, jobEvent, selectedJobID));
    }
  }, [applyJobSnapshot, selectedJobID]);

  async function refresh() {
    setBusy(true);
    try {
      const [accountsData, jobsData, mailboxesData, allocationsData, runningJobsData] = await Promise.all([
        api<Account[]>(`/api/accounts?limit=200${accountStatus ? `&status=${accountStatus}` : ''}`),
        api<JobSnapshot[]>(`/api/jobs?limit=200${jobStatus ? `&status=${jobStatus}` : ''}`),
        api<Mailbox[]>('/api/mailboxes?limit=500'),
        api<GPTEmailAllocation[]>('/api/gpt-email-allocations?limit=500'),
        api<JobSnapshot[]>('/api/jobs?limit=200&status=RUNNING')
      ]);
      setAccounts(Array.isArray(accountsData) ? accountsData : []);
      setJobSnapshots(Array.isArray(jobsData) ? jobsData : []);
      setRunningJobSnapshots(Array.isArray(runningJobsData) ? runningJobsData : []);
      const nextMailboxes = Array.isArray(mailboxesData) ? mailboxesData : [];
      setMailboxes(nextMailboxes);
      setGptEmailAllocations(Array.isArray(allocationsData) ? allocationsData : []);
      if (selectedJob) {
        await refreshSelectedJob(selectedJob.job_id);
      }
      if (selectedMailbox) {
        const freshMailbox = nextMailboxes.find((mailbox) => mailbox.email_address === selectedMailbox.email_address);
        if (freshMailbox) setSelectedMailbox(freshMailbox);
      }
      if (activeView === 'gopay') {
        await loadGoPayStateStatus(false);
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

  async function copyField(label: string, value: string) {
    const copied = await copyText(value);
    setToast({
      kind: copied ? 'ok' : 'error',
      text: copied ? `${label}已复制` : `${label}复制失败，浏览器拒绝访问剪贴板`
    });
  }

  async function loadGoPayStateStatus(showToast = false) {
    setGoPayStateLoading(true);
    try {
      const resp = await api<GoPayDashboardStateResponse>('/api/gopay/state?user_id=local');
      setGoPayStateStatus(resp);
      if (showToast) {
        setToast(resp.error_message
          ? { kind: 'error', text: resp.error_message }
          : { kind: 'ok', text: 'GoPay state 已刷新' });
      }
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
    } finally {
      setGoPayStateLoading(false);
    }
  }

  async function runGoPayPayment(account: Account, otpChannel: ConcreteGoPayPaymentChannel) {
    setBusy(true);
    try {
      const payload: Record<string, any> = {
        account_id: account.account_id,
        user_id: 'local',
        otp_channel: otpChannel
      };
      const resp = await api<any>('/api/workflows/gopay-payment', {
        method: 'POST',
        body: JSON.stringify(payload)
      });
      if (resp.error_message) {
        setToast({ kind: 'error', text: resp.error_message });
      } else {
        setToast({ kind: 'ok', text: `${goPayPaymentChannelLabel(otpChannel)} 支付已提交: ${resp.job_id || 'ok'}` });
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

  async function confirmManualAddBalance(job: Job) {
    try {
      const resp = await api<ManualAddBalanceConfirmResponse>(`/api/jobs/${job.job_id}/add-balance/confirm`, {
        method: 'POST',
        body: '{}'
      });
      if (resp.error_message || !resp.success) {
        setToast({ kind: 'error', text: resp.error_message || '转账确认失败' });
        return;
      }
      setToast({ kind: 'ok', text: `转账已确认: ${short(resp.job_id || job.job_id)}` });
      await refreshSelectedJob(job.job_id);
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
      throw err;
    }
  }

  async function selectGoPayAddBalance(job: Job, method: ConcreteGoPayAddBalanceMethod) {
    try {
      const resp = await api<ManualAddBalanceConfirmResponse>(`/api/jobs/${job.job_id}/add-balance/select`, {
        method: 'POST',
        body: JSON.stringify({ add_balance: goPayAddBalancePayload(method) })
      });
      if (resp.error_message || !resp.success) {
        setToast({ kind: 'error', text: resp.error_message || '加余额方式选择失败' });
        return;
      }
      setToast({ kind: 'ok', text: `已选择${addBalanceMethodLabel(method)}: ${short(resp.job_id || job.job_id)}` });
      await refreshSelectedJob(job.job_id);
    } catch (err) {
      setToast({ kind: 'error', text: errorText(err) });
      throw err;
    }
  }

  async function retryGoPayPaymentRebind(job: Job) {
    try {
      const result = objectValue(job.result);
      const sourceJobId = job.action === 'GOPAY_PAYMENT_REBIND' ? stringValue(result.source_job_id) : job.job_id;
      const resp = await api<any>('/api/workflows/gopay-payment/rebind', {
        method: 'POST',
        body: JSON.stringify({
          source_job_id: sourceJobId,
          account_id: job.account_id || '',
          user_id: goPayPaymentUserId(job)
        })
      });
      if (resp.error_message) {
        setToast({ kind: 'error', text: resp.error_message });
        return;
      }
      setToast({ kind: 'ok', text: `换绑重试已提交: ${resp.job_id || 'ok'}` });
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

  async function updateAccount(account: Account, payload: { session_token?: string; access_token?: string; activation_channel?: string }, successText: string) {
    setBusy(true);
    try {
      const updated = await api<Account>(`/api/accounts/${account.account_id}`, {
        method: 'PATCH',
        body: JSON.stringify(payload)
      });
      setAccounts((prev) => prev.map((item) => item.account_id === updated.account_id ? updated : item));
      setSelectedAccount(updated);
      setToast({ kind: 'ok', text: successText });
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
  }, [accountStatus, jobStatus]);

  useEffect(() => {
    if (!runningJobIDsKey) {
      return;
    }
    const params = new URLSearchParams();
    runningJobIDsKey.split('|').forEach((jobID) => params.append('job_id', jobID));
    const source = new EventSource(`/api/jobs/events?${params.toString()}`);
    source.addEventListener('job', (event) => {
      const jobEvent = JSON.parse((event as MessageEvent).data) as JobEvent;
      applyJobEvent(jobEvent);
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
    return () => {
      source.close();
    };
  }, [runningJobIDsKey, applyJobEvent]);

  useEffect(() => {
    if (!selectedJobID) {
      setSelectedJobEvents([]);
      return;
    }
    setSelectedJobEvents([]);
    const params = new URLSearchParams();
    params.append('job_id', selectedJobID);
    const source = new EventSource(`/api/jobs/events?${params.toString()}`);
    source.addEventListener('job', (event) => {
      const jobEvent = JSON.parse((event as MessageEvent).data) as JobEvent;
      applyJobEvent(jobEvent);
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
    return () => {
      source.close();
    };
  }, [selectedJobID, applyJobEvent]);

  useEffect(() => {
    if (!selectedJob || selectedJob.status !== 'RUNNING') return;
    const id = window.setInterval(() => setNowUnix(Math.floor(Date.now() / 1000)), 1000);
    return () => window.clearInterval(id);
  }, [selectedJob?.job_id, selectedJob?.status]);

  useEffect(() => {
    if (activeView !== 'gopay') return;
    void loadGoPayStateStatus(false);
  }, [activeView]);

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
    setSelectedJobEvents([]);
    setSelectedMailbox(null);
  }

  function openView(view: ViewKey) {
    setActiveView(view);
    closeDetails();
  }

  const primaryMailboxes = mailboxes.filter((mailbox) => mailbox.is_primary);
  const visiblePrimaryMailboxes = primaryMailboxes.filter((mailbox) => mailboxMatchesFilter(mailbox, mailboxes, gptEmailAllocations, mailboxStatus));
  const allocatableMailboxCount = countAllocatableEmailAllocations(gptEmailAllocations);
  const missingOAuthCount = primaryMailboxes.filter((mailbox) => (
    mailbox.password && authStatus(mailbox) !== 'AUTHORIZED' && authStatus(mailbox) !== 'NEEDS_MANUAL_VERIFICATION'
  )).length;
  const oauthMailboxCount = primaryMailboxes.filter((mailbox) => authStatus(mailbox) === 'AUTHORIZED').length;
  const selectedMailboxInbox = selectedMailbox ? inboxResultForMailbox(inboxResponse, selectedMailbox.email_address) : undefined;
  const selectedMailboxBans = selectedMailbox ? bansForMailbox(inboxResponse, selectedMailbox.email_address) : [];
  const selectedMailboxAliases = selectedMailbox ? aliasesForMailbox(mailboxes, selectedMailbox) : [];
  const selectedMailboxAllocation = selectedMailbox ? allocationForEmail(gptEmailAllocations, selectedMailbox.email_address) : undefined;
  const selectedAccountMailboxContext = selectedAccount ? mailboxContextForEmail(mailboxes, gptEmailAllocations, selectedAccount.email) : null;
  const selectedAccountLatestOtp = selectedAccount ? latestOtpForEmail(inboxResponse, mailboxes, selectedAccount.email) : null;
  const gptWorkflowJobs = jobs.filter((job) => gptWorkflowActions.has(job.action));
  const gopayWorkflowJobs = jobs.filter((job) => gopayWorkflowActions.has(job.action));
  const goPayRebindJobs = gopayWorkflowJobs.filter((job) => job.action === 'GOPAY_PAYMENT_REBIND');
  const mailboxWorkflowJobs = jobs.filter((job) => mailboxWorkflowActions.has(job.action));
  const mailboxRegisterJobs = mailboxWorkflowJobs.filter((job) => job.action === 'REGISTER_MAILBOX');
  const runningMailboxRegisterCount = runningJobs.filter((job) => job.action === 'REGISTER_MAILBOX').length;
  const runningGoPayRebindCount = runningJobs.filter((job) => job.action === 'GOPAY_PAYMENT_REBIND').length;
  const jobsForWorkflowTab = workflowTab === 'gpt'
    ? gptWorkflowJobs
    : workflowTab === 'gopay'
      ? gopayWorkflowJobs
      : workflowTab === 'mailbox'
        ? mailboxWorkflowJobs
        : jobs;
  const latestMailboxRegisterJob = mailboxRegisterJobs[0];
  const latestGoPayRebindJob = goPayRebindJobs[0];
  const panelState: PanelState = {
    loading: busy && accounts.length === 0 && jobs.length === 0 && mailboxes.length === 0,
    error: loadError
  };

  return (
    <main className="shell">
      <header className="topbar">
        <div>
          <h1>NB Register</h1>
          <p>GPT 账号、邮箱和工作流控制台</p>
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
          <NavItem active={activeView === 'accounts'} icon={<OpenAIIcon size={17} />} label="GPT账号" count={accounts.length} countLabel="全部 GPT 账号数" onClick={() => openView('accounts')} />
          <NavItem active={activeView === 'gopay'} icon={<RefreshCcw size={17} />} label="GoPay" count={runningGoPayRebindCount} countLabel="运行中的 GoPay 换绑任务" onClick={() => openView('gopay')} />
          <NavItem active={activeView === 'mailboxes'} icon={<Inbox size={17} />} label="邮箱管理" count={allocatableMailboxCount} countLabel="可分配邮箱源数" onClick={() => openView('mailboxes')} />
          <NavItem active={activeView === 'jobs'} icon={<ListChecks size={17} />} label="工作流" count={runningJobCount} countLabel="运行中的工作流任务" onClick={() => openView('jobs')} />
        </nav>

        <div className="contentPane">
          {activeView === 'accounts' && (
            <section className="metrics">
              <Metric label="GPT账号" value={accounts.length} hint="当前 GPT 账号池总量" icon={<OpenAIIcon />} />
              <Metric label="已激活" value={accounts.filter((a) => a.status === 'ACTIVATED').length} hint="可进入后续使用的账号" icon={<Zap />} />
              <Metric label="可分配邮箱源" value={allocatableMailboxCount} hint="可用主邮箱、可用分裂邮箱、可分裂主邮箱" icon={<Mail />} />
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
                <PanelHeader title="GPT账号管理" icon={<Search size={16} />}>
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
                  jobs={jobs}
                  selected={selectedAccount?.account_id}
                  showSecrets={showSecrets}
                  runningAccountIds={runningAccountIds}
                  runningWorkflowByAccountID={runningJobByAccountID}
                  refreshingAccessTokenIds={refreshingAccessTokenIds}
                  busy={busy}
                  onSelect={selectAccount}
                  onOpenWorkflow={selectJob}
                  onRegister={(account) => runAccountWorkflow('注册账号', '/api/workflows/register', account)}
                  onLogin={(account) => runAccountWorkflow(loginActionLabel(account), '/api/workflows/login', account)}
                  onGoPayPayment={(account, channel) => void runGoPayPayment(account, channel)}
                  onProbeAccount={(account) => runAccountWorkflow('探测账号', '/api/workflows/probe', account)}
                  onRegisterActivate={(account) => runAccountWorkflow('注册并激活', '/api/workflows/register-and-activate', account)}
                  onRefreshAccessToken={refreshAccountAccessToken}
                  onDelete={deleteAccount}
                />
              </div>
            </section>
          )}

          {activeView === 'gopay' && (
            <section className="workspace jobsWorkspace">
              <div className="panel jobsPanel">
                <PanelHeader title="GoPay 换绑" icon={<RefreshCcw size={16} />}>
                  <Button className="secondaryButton" onClick={() => void loadGoPayStateStatus(true)} disabled={goPayStateLoading}>
                    <RefreshCcw size={16} /> {goPayStateLoading ? '刷新中' : '刷新 state'}
                  </Button>
                </PanelHeader>
                <GoPayStateStatusPanel state={goPayStateStatus} loading={goPayStateLoading} />
                <div className="workflowTabToolbar goPayRebindToolbar">
                  <WorkflowSummary
                    job={latestGoPayRebindJob}
                    runningCount={runningGoPayRebindCount}
                    runningTitle={(count) => `${count} 个换绑任务运行中`}
                    runningText="GoPay WA 支付完成后会自动创建换绑任务。"
                    idleTitle="暂无换绑任务"
                    idleText="支付任务不在 GoPay 页展示。"
                  />
                </div>
                <JobTable jobs={goPayRebindJobs} selected={selectedJob?.job_id} emptyText="暂无 GoPay 换绑任务" onSelect={selectJob} onGoPayRebindRetry={retryGoPayPaymentRebind} />
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
                  allocations={gptEmailAllocations}
                  selected={selectedMailbox?.email_address}
                  busy={busy}
                  showSecrets={showSecrets}
                  oauthing={mailboxOAuthing}
                  runningWorkflowByEmail={runningMailboxJobByEmail}
                  onSelect={selectMailbox}
                  onOpenWorkflow={selectJob}
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

	          {activeView === 'jobs' && (
            <section className="workspace jobsWorkspace">
              <div className="panel jobsPanel">
                <PanelHeader title="工作流" icon={<Activity size={16} />}>
                  <NativeSelect value={jobStatus} onChange={(e) => setJobStatus(e.target.value)}>
                    {jobStatusOptions.map((s) => <NativeSelectOption key={s} value={s}>{s ? statusText(s) : '全部状态'}</NativeSelectOption>)}
                  </NativeSelect>
                </PanelHeader>
                <Tabs value={workflowTab} onValueChange={(value) => setWorkflowTab(value as WorkflowTab)} className="workflowTabs">
                  <TabsList className="workflowTabList">
                    <TabsTrigger value="all">全部 {jobs.length}</TabsTrigger>
                    <TabsTrigger value="gpt">GPT账号 {gptWorkflowJobs.length}</TabsTrigger>
                    <TabsTrigger value="gopay">GoPay {gopayWorkflowJobs.length}</TabsTrigger>
                    <TabsTrigger value="mailbox">邮箱 {mailboxWorkflowJobs.length}</TabsTrigger>
                  </TabsList>

                  <TabsContent value="all" className="workflowTabContent">
                    <JobTable jobs={jobsForWorkflowTab} selected={selectedJob?.job_id} emptyText="暂无工作流任务" onSelect={selectJob} onGoPayRebindRetry={retryGoPayPaymentRebind} />
                  </TabsContent>

                  <TabsContent value="gpt" className="workflowTabContent">
                    <JobTable jobs={jobsForWorkflowTab} selected={selectedJob?.job_id} emptyText="暂无 GPT 账号工作流" onSelect={selectJob} onGoPayRebindRetry={retryGoPayPaymentRebind} />
                  </TabsContent>

                  <TabsContent value="gopay" className="workflowTabContent">
                    <JobTable jobs={jobsForWorkflowTab} selected={selectedJob?.job_id} emptyText="暂无 GoPay 工作流" onSelect={selectJob} onGoPayRebindRetry={retryGoPayPaymentRebind} />
                  </TabsContent>

                  <TabsContent value="mailbox" className="workflowTabContent mailboxWorkflowTab">
                    <div className="workflowTabToolbar">
                      <WorkflowSummary
                        job={latestMailboxRegisterJob}
                        runningCount={runningMailboxRegisterCount}
                        runningTitle={(count) => `${count} 个邮箱注册任务运行中`}
                        runningText="邮箱注册器同一时间只跑一个进程。"
                        idleTitle="暂无邮箱注册任务"
                        idleText="还没有启动过邮箱注册。"
                      />
                      <Button className="primaryButton" onClick={startMailboxRegistration} disabled={busy || mailboxRegistering}>
                        <Play size={16} /> {mailboxRegistering ? '启动中' : '启动注册'}
                      </Button>
                    </div>
                    <MailboxStatusStrip mailboxes={primaryMailboxes} allocations={gptEmailAllocations} />
                    <JobTable jobs={jobsForWorkflowTab} selected={selectedJob?.job_id} emptyText="暂无邮箱工作流" onSelect={selectJob} onGoPayRebindRetry={retryGoPayPaymentRebind} />
                  </TabsContent>
                </Tabs>
              </div>
            </section>
          )}
        </div>
      </section>

      <DetailDrawer open={!!selectedAccount} title="GPT账号详情" onClose={closeDetails}>
        {selectedAccount && (
          <AccountDetails
            account={selectedAccount}
            showSecrets={showSecrets}
            busy={busy}
            inboxLoading={inboxLoading}
            mailboxContext={selectedAccountMailboxContext}
            latestOtp={selectedAccountLatestOtp}
            activationChannel={accountActivationChannel(selectedAccount, jobs)}
            onCopy={copyField}
            onFetchInbox={fetchMailboxInbox}
            onSessionSave={(account, sessionToken) => updateAccount(account, { session_token: sessionToken }, '认证信息已更新')}
            onAccessSave={(account, accessToken) => updateAccount(account, { access_token: accessToken }, '认证信息已更新')}
            onActivationChannelSave={(account, activationChannel) => updateAccount(account, { activation_channel: activationChannel }, '渠道已更新')}
	            onProbeAccount={(account) => runAccountWorkflow('探测账号', '/api/workflows/probe', account)}
	            onLogin={(account) => runAccountWorkflow(loginActionLabel(account), '/api/workflows/login', account)}
            onRefreshAccessToken={refreshAccountAccessToken}
            refreshingAccessToken={refreshingAccessTokenIds.has(selectedAccount.account_id)}
          />
        )}
      </DetailDrawer>

      <WorkflowDialog open={!!selectedJob} onClose={closeDetails}>
        {selectedJob && (
          <JobDetails
            job={selectedJob}
            progress={selectedJobProgress}
            events={selectedJobEvents}
	            nowUnix={nowUnix}
	            onCopy={copyField}
	            onOtpSubmit={submitJobOtp}
	            onManualAddBalanceConfirm={confirmManualAddBalance}
	            onGoPayAddBalanceSelect={selectGoPayAddBalance}
	            onGoPayRebindRetry={retryGoPayPaymentRebind}
	          />
        )}
      </WorkflowDialog>

      <DetailDrawer open={!!selectedMailbox} title="邮箱详情" onClose={closeDetails}>
        {selectedMailbox && (
          <MailboxDetails
            mailbox={selectedMailbox}
            showSecrets={showSecrets}
            inboxResult={selectedMailboxInbox}
            bans={selectedMailboxBans}
            aliases={selectedMailboxAliases}
            allocation={selectedMailboxAllocation}
            allocations={gptEmailAllocations}
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

function OpenAIIcon({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d="M12 3.5a4.2 4.2 0 0 1 3.73 2.28l.43.82.92.08a4.2 4.2 0 0 1 2.09 7.73l-.78.49.04.92a4.2 4.2 0 0 1-5.86 4.03L12 19.5l-.57.35a4.2 4.2 0 0 1-5.86-4.03l.04-.92-.78-.49a4.2 4.2 0 0 1 2.09-7.73l.92-.08.43-.82A4.2 4.2 0 0 1 12 3.5Z"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinejoin="round"
      />
      <path
        d="M8.15 8.55 12 10.78l3.85-2.23M8.15 15.45 12 13.22l3.85 2.23M8.15 8.55v6.9M15.85 8.55v6.9M12 10.78v4.44"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
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

function WorkflowDialog({ open, onClose, children }: {
  open: boolean;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={(nextOpen) => {
      if (!nextOpen) onClose();
    }}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="workflowDialogOverlay" />
        <DialogPrimitive.Content className="workflowDialogContent">
          <div className="workflowDialogHeader">
            <DialogPrimitive.Title className="drawerTitle"><Activity size={16} />工作流详情</DialogPrimitive.Title>
            <DialogPrimitive.Description className="sr-only">工作流详情弹窗</DialogPrimitive.Description>
            <DialogPrimitive.Close asChild>
              <Button className="iconButton" {...buttonHint('关闭工作流详情')}>
                <X size={16} />
              </Button>
            </DialogPrimitive.Close>
          </div>
          <div className="workflowDialogBody">
            {children}
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

function AccountDetails({ account, showSecrets, busy, inboxLoading, refreshingAccessToken, mailboxContext, latestOtp, activationChannel, onCopy, onFetchInbox, onSessionSave, onAccessSave, onActivationChannelSave, onProbeAccount, onLogin, onRefreshAccessToken }: {
  account: Account;
  showSecrets: boolean;
  busy: boolean;
  inboxLoading: boolean;
  refreshingAccessToken: boolean;
  mailboxContext: AccountMailboxContext | null;
  latestOtp: LatestOtp | null;
  activationChannel: string;
  onCopy: (label: string, value: string) => void;
  onFetchInbox: (emailAddress?: string) => Promise<void>;
  onSessionSave: (account: Account, sessionToken: string) => Promise<void>;
  onAccessSave: (account: Account, accessToken: string) => Promise<void>;
  onActivationChannelSave: (account: Account, activationChannel: string) => Promise<void>;
  onProbeAccount: (account: Account) => void;
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
            <Button {...buttonHint(accountInboxHint(account.email, mailboxContext, showSecrets))} disabled={busy || inboxLoading || !account.email} onClick={() => void onFetchInbox(account.email)}>
              <Inbox size={14} /> {inboxLoading ? '拉取中' : '拉取 OTP'}
            </Button>
          </div>
        </div>
        <AccountOtpPanel latestOtp={latestOtp} showSecrets={showSecrets} loading={inboxLoading} onCopy={onCopy} />
        <KV label="ID" value={account.account_id} mono onCopy={onCopy} />
        <KV label="状态" value={statusText(account.status)} copyValue={account.status || '-'} onCopy={onCopy} />
        <KV label="Tier" value={tierText(account.tier)} />
        <KV label="Plus资格" value={plusText(account)} />
        <ActivationChannelEditor account={account} activationChannel={activationChannel} onSave={onActivationChannelSave} />
        <KV label="邮箱" value={showSecrets ? account.email : maskEmail(account.email)} copyValue={account.email} copyDisabled={!account.email} masked={!showSecrets} onCopy={onCopy} />
        <KV label="密码" value={showSecrets ? account.password : mask(account.password)} copyValue={account.password} copyDisabled={!account.password} masked={!showSecrets} mono onCopy={onCopy} />
        <TokenEditor label="Session" field="session_token" account={account} showSecrets={showSecrets} onCopy={onCopy} onSave={onSessionSave} />
        <TokenEditor label="Access" field="access_token" account={account} showSecrets={showSecrets} onCopy={onCopy} onSave={onAccessSave} />
        <KV label="创建时间" value={formatUnix(account.created_at)} onCopy={onCopy} />
        <KV label="更新时间" value={formatUnix(account.updated_at)} onCopy={onCopy} />
      </section>
    </div>
  );
}

function AccountOtpPanel({ latestOtp, showSecrets, loading, onCopy }: {
  latestOtp: LatestOtp | null;
  showSecrets: boolean;
  loading: boolean;
  onCopy: (label: string, value: string) => void;
}) {
  const hasOtp = !!latestOtp?.otp;
  const subject = latestOtp?.subject || 'Latest OTP';
  const displaySubject = showSecrets ? subject : maskPreview(subject);
  const code = hasOtp ? latestOtp.otp : '';
  const receivedAt = latestOtp?.received_at_unix || 0;

  return (
    <div className={`accountOtpPanel${hasOtp ? ' hasOtp' : ''}`} role="status" aria-live="polite">
      <div>
        <span>{loading ? '正在拉取 OTP' : '最近 OTP'}</span>
        <strong className={hasOtp ? 'mono' : ''}>{hasOtp ? (showSecrets ? code : mask(code)) : '暂无 OTP'}</strong>
        <small title={displaySubject}>
          {hasOtp ? `${formatUnix(receivedAt)} · ${displaySubject}` : '点击拉取 OTP 后在这里显示最新验证码'}
        </small>
      </div>
      <Button className="copyButton" {...buttonHint('复制 OTP')} disabled={!hasOtp} onClick={() => onCopy('OTP', code)}>
        <Copy size={14} />
      </Button>
    </div>
  );
}

function JobDetails({ job, progress, events, nowUnix, onCopy, onOtpSubmit, onManualAddBalanceConfirm, onGoPayAddBalanceSelect, onGoPayRebindRetry }: {
  job: Job;
  progress: WorkflowProgress | null;
  events: JobEvent[];
  nowUnix: number;
  onCopy: (label: string, value: string) => void;
  onOtpSubmit: (job: Job, otp: string) => Promise<void>;
  onManualAddBalanceConfirm: (job: Job) => Promise<void>;
  onGoPayAddBalanceSelect: (job: Job, method: ConcreteGoPayAddBalanceMethod) => Promise<void>;
  onGoPayRebindRetry: (job: Job) => Promise<void>;
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
	        <ManualAddBalancePanel
	          job={job}
	          progress={progress}
	          onConfirm={onManualAddBalanceConfirm}
	          onSelect={onGoPayAddBalanceSelect}
	          onCopy={onCopy}
	        />
          <GoPayRebindPanel job={job} onRetry={onGoPayRebindRetry} />
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
                    {stepDuration(step, nowUnix)}
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
                {step.detail && (
                  <details className="jsonDetails">
                    <summary>结果数据</summary>
                    <pre>{formatJSON(step.detail)}</pre>
                  </details>
                )}
              </div>
            );
          })}
          {(!job.steps || job.steps.length === 0) && <EmptyBlock text="暂无步骤明细。" />}
        </div>
        <WorkflowEvents events={events} />
      </section>
    </div>
  );
}

function WorkflowEvents({ events }: { events: JobEvent[] }) {
  return (
    <div className="workflowEvents">
      <div className="sectionTitle">
        <h3>事件</h3>
        <span className="muted">{events.length}</span>
      </div>
      <div className="eventList">
        {events.length === 0 && <EmptyBlock text="暂无事件流数据。" />}
        {events.slice(0, 30).map((event) => {
          const snapshot = event.snapshot;
          const job = snapshot?.job;
          const progress = snapshot?.progress;
          const step = progress?.step_name || job?.last_step || '';
          const status = progress?.status || job?.status || '';
          return (
            <div className="eventItem" key={event.event_id}>
              <div>
                <strong>{eventText(event.event_type)}</strong>
                <span>{step ? stepText(step) : '-'}</span>
              </div>
              <small>{status ? statusText(status.toUpperCase()) : '-'} · {eventTime(event)}</small>
            </div>
          );
        })}
      </div>
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

function ManualAddBalancePanel({ job, progress, onConfirm, onSelect, onCopy }: {
  job: Job;
  progress: WorkflowProgress | null;
  onConfirm: (job: Job) => Promise<void>;
  onSelect: (job: Job, method: ConcreteGoPayAddBalanceMethod) => Promise<void>;
  onCopy: (label: string, value: string) => void;
}) {
  const [submitting, setSubmitting] = useState(false);
  const [selecting, setSelecting] = useState('');
  const balance = manualAddBalanceView(job);
  const canSelect = canSelectGoPayAddBalance(job, progress, balance);
  if (!canSelect && (!balance || balance.method !== 'manual_transfer')) return null;

  const transfer = balance?.transfer || { qr_payload: '', instructions: '', amount: 0, currency: 'IDR' };
  const canConfirm = canConfirmManualAddBalance(job, progress, balance);
  async function select(method: ConcreteGoPayAddBalanceMethod) {
    setSelecting(method);
    try {
      await onSelect(job, method);
    } finally {
      setSelecting('');
    }
  }

  async function confirm() {
    setSubmitting(true);
    try {
      await onConfirm(job);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="manualBalancePanel">
      <div className="manualBalanceHead">
        <span><QrCode size={15} /> {canSelect ? '选择加余额方式' : '手动转账'}</span>
        <StatusBadge status={canSelect || canConfirm ? 'RUNNING' : balance?.status === 'confirmed' ? 'SUCCEEDED' : 'RUNNING'} />
      </div>
      {canSelect && (
        <div className="addBalanceChoiceList">
          {GO_PAY_ADD_BALANCE_METHODS.map((method) => (
            <Button
              key={method}
              className={method === 'manual_transfer' ? 'secondaryButton' : 'primaryButton'}
              disabled={!!selecting}
              onClick={() => void select(method)}
            >
              <WalletCards size={14} /> {selecting === method ? '选择中' : addBalanceMethodLabel(method)}
            </Button>
          ))}
        </div>
      )}
      {!canSelect && (
      <div className="transferBox">
        <TransferQRCode payload={transfer.qr_payload} />
        <div className="transferMeta">
          <strong>{transfer.amount > 0 ? `${transfer.amount} ${transfer.currency || 'IDR'}` : (transfer.currency || 'IDR')}</strong>
          <span>{transfer.instructions || '转账后点击确认继续流程。'}</span>
          <div className="transferActions">
            <Button className="copyButton" {...buttonHint('复制二维码内容')} disabled={!transfer.qr_payload} onClick={() => onCopy('二维码内容', transfer.qr_payload)}>
              <Copy size={14} />
            </Button>
            <Button className="primaryButton" disabled={!canConfirm || submitting} onClick={() => void confirm()}>
              <CheckCircle2 size={14} /> 已转账，继续
            </Button>
          </div>
        </div>
      </div>
      )}
    </div>
  );
}

function GoPayRebindPanel({ job, onRetry }: {
  job: Job;
  onRetry: (job: Job) => Promise<void>;
}) {
  const [submitting, setSubmitting] = useState(false);
  if (!canRetryGoPayPaymentRebind(job)) return null;

  async function retry() {
    setSubmitting(true);
    try {
      await onRetry(job);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="manualBalancePanel">
      <div className="manualBalanceHead">
        <span><RefreshCcw size={15} /> 支付后换绑</span>
        <StatusBadge status={job.status} />
      </div>
      <div className="rebindRetryBox">
        <span>支付已完成，可单独执行 SMS 换绑。</span>
        <Button className="primaryButton" disabled={submitting} onClick={() => void retry()}>
          <RefreshCcw size={14} /> 开始换绑
        </Button>
      </div>
    </div>
  );
}

function TransferQRCode({ payload }: { payload: string }) {
  const [dataUrl, setDataUrl] = useState('');

  useEffect(() => {
    let cancelled = false;
    setDataUrl('');
    if (!payload) return () => {
      cancelled = true;
    };
    QRCode.toDataURL(payload, { width: 224, margin: 1, errorCorrectionLevel: 'M' })
      .then((value) => {
        if (!cancelled) setDataUrl(value);
      })
      .catch(() => {
        if (!cancelled) setDataUrl('');
      });
    return () => {
      cancelled = true;
    };
  }, [payload]);

  if (dataUrl) {
    return <img className="transferQr" src={dataUrl} alt="GoPay 转账二维码" />;
  }
  return (
    <div className="transferQr transferQrEmpty">
      <QrCode size={34} />
      <span>未配置二维码</span>
    </div>
  );
}

function AccountTable({ accounts, jobs, selected, showSecrets, runningAccountIds, runningWorkflowByAccountID, refreshingAccessTokenIds, busy, onSelect, onOpenWorkflow, onRegister, onLogin, onGoPayPayment, onProbeAccount, onRegisterActivate, onRefreshAccessToken, onDelete }: {
  accounts: Account[];
  jobs: Job[];
  selected?: string;
  showSecrets: boolean;
  runningAccountIds: Set<string>;
  runningWorkflowByAccountID: Map<string, Job>;
  refreshingAccessTokenIds: Set<string>;
  busy: boolean;
  onSelect: (a: Account) => void;
  onOpenWorkflow: (job: Job) => void;
  onRegister: (a: Account) => void;
  onLogin: (a: Account) => void;
  onGoPayPayment: (a: Account, channel: ConcreteGoPayPaymentChannel) => void;
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
            <TableHead>Plus资格</TableHead>
            <TableHead>渠道</TableHead>
            <TableHead>更新</TableHead>
            <TableHead>操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {accounts.length === 0 && <EmptyTableRow colSpan={8} text="暂无账号。可以先创建账号，或切换为全部状态查看。" />}
          {accounts.map((account) => {
            const accountBusy = runningAccountIds.has(account.account_id);
            const currentWorkflow = runningWorkflowByAccountID.get(account.account_id);
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
                <TableCell data-label="Plus资格"><PlusBadge account={account} /></TableCell>
            <TableCell data-label="渠道">
                  <span className="activationChannel">{accountActivationChannel(account, jobs)}</span>
                </TableCell>
                <TableCell data-label="更新">{formatUnix(account.updated_at)}</TableCell>
                <TableCell data-label="操作">
                  <AccountRowActions
                    account={account}
                    accountBusy={accountBusy}
                    currentWorkflow={currentWorkflow}
                    busy={busy}
                    refreshingAccessToken={refreshingAccessToken}
                    onOpenWorkflow={onOpenWorkflow}
                    onRegister={onRegister}
                    onLogin={onLogin}
                    onGoPayPayment={onGoPayPayment}
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

function AccountRowActions({ account, accountBusy, currentWorkflow, busy, refreshingAccessToken, onOpenWorkflow, onRegister, onLogin, onGoPayPayment, onProbeAccount, onRegisterActivate, onRefreshAccessToken, onDelete }: {
  account: Account;
  accountBusy: boolean;
  currentWorkflow?: Job;
  busy: boolean;
  refreshingAccessToken: boolean;
  onOpenWorkflow: (job: Job) => void;
  onRegister: (a: Account) => void;
  onLogin: (a: Account) => void;
  onGoPayPayment: (a: Account, channel: ConcreteGoPayPaymentChannel) => void;
  onProbeAccount: (a: Account) => void;
  onRegisterActivate: (a: Account) => void;
  onRefreshAccessToken: (a: Account) => Promise<void>;
  onDelete: (a: Account) => void;
}) {
  if (accountBusy && currentWorkflow && !isUserAlreadyExistsAccount(account)) {
    return (
      <div className="rowActions" onClick={(event) => event.stopPropagation()}>
        <LinkedWorkflowButton job={currentWorkflow} onOpen={onOpenWorkflow} />
      </div>
    );
  }

  const actions: RowActionDescriptor[] = [];
  if (canRegister(account)) actions.push({ label: '注册账号', icon: <Play size={14} />, onClick: () => onRegister(account), disabled: busy, kind: 'primary' });
  if (canRefreshAccessToken(account)) actions.push({ label: refreshingAccessToken ? '获取中' : '获取 Access', icon: <KeyRound size={14} />, onClick: () => void onRefreshAccessToken(account), disabled: busy || refreshingAccessToken, kind: actions.length ? 'secondary' : 'primary' });
  if (canLoginSession(account)) actions.push({ label: loginActionLabel(account), icon: <KeyRound size={14} />, onClick: () => onLogin(account), disabled: busy, kind: actions.length ? 'secondary' : 'primary' });
  if (canProbeAccount(account)) actions.push({ label: '探测账号', icon: <Search size={14} />, onClick: () => onProbeAccount(account), disabled: busy, kind: 'secondary' });
  if (canRegister(account)) actions.push({ label: '注册并激活', icon: <ShieldCheck size={14} />, onClick: () => onRegisterActivate(account), disabled: busy, kind: 'secondary' });
  actions.push({ label: '删除账号', icon: <Trash2 size={14} />, onClick: () => onDelete(account), disabled: busy, kind: 'danger' });

  const paymentActions: RowActionDescriptor[] = canGoPayPayment(account)
    ? GO_PAY_PAYMENT_CHANNELS.map((channel) => ({
      label: goPayPaymentChannelLabel(channel),
      icon: <WalletCards size={14} />,
      onClick: () => onGoPayPayment(account, channel),
      disabled: busy,
      kind: 'secondary' as const
    }))
    : [];

  const primary = actions.find((action) => action.kind === 'primary' && !action.disabled) ||
    actions.find((action) => !action.disabled) ||
    actions[0];
  const secondary = actions.filter((action) => action !== primary);

  return (
    <div className="rowActions" onClick={(event) => event.stopPropagation()}>
      <div className="rowActionsMain">
        <RowActionButton action={primary} showLabel />
        {secondary.map((action) => <RowActionButton key={action.label} action={action} />)}
      </div>
      {paymentActions.length > 0 && (
        <div className="rowActionsPayment">
          {paymentActions.map((action) => <RowActionButton key={action.label} action={action} showLabel fullLabel />)}
        </div>
      )}
    </div>
  );
}

function RowActionButton({ action, showLabel, fullLabel }: { action: RowActionDescriptor; showLabel?: boolean; fullLabel?: boolean }) {
  const className = [
    showLabel ? 'rowButtonText' : 'iconButton',
    fullLabel ? 'rowPaymentButton' : '',
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

function LinkedWorkflowButton({ job, onOpen }: { job: Job; onOpen: (job: Job) => void }) {
  return (
    <Button className="rowButtonText linkedWorkflowButton" {...buttonHint(`查看工作流：${actionText(job.action)}`)} onClick={() => onOpen(job)}>
      <Activity size={14} /> 工作流
    </Button>
  );
}

function JobTable({ jobs, selected, emptyText = '暂无工作流任务', onSelect, onGoPayRebindRetry }: {
  jobs: Job[];
  selected?: string;
  emptyText?: string;
  onSelect: (j: Job) => void;
  onGoPayRebindRetry?: (job: Job) => Promise<void>;
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
              <TableCell data-label="操作">
                {onGoPayRebindRetry && canRetryGoPayPaymentRebind(job)
                  ? <GoPayRebindRowButton job={job} onRetry={onGoPayRebindRetry} />
                  : <span className="muted">-</span>}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function GoPayRebindRowButton({ job, onRetry }: { job: Job; onRetry: (job: Job) => Promise<void> }) {
  const [submitting, setSubmitting] = useState(false);
  async function retry(event: React.MouseEvent) {
    event.stopPropagation();
    setSubmitting(true);
    try {
      await onRetry(job);
    } finally {
      setSubmitting(false);
    }
  }
  return (
    <Button className="rowButtonText linkedWorkflowButton" disabled={submitting} {...buttonHint(job.action === 'GOPAY_PAYMENT_REBIND' ? '重试换绑' : '执行换绑')} onClick={(event) => void retry(event)}>
      <RefreshCcw size={14} /> <span>{job.action === 'GOPAY_PAYMENT_REBIND' ? '重试换绑' : '换绑'}</span>
    </Button>
  );
}

function MailboxPanel({ mailboxes, allMailboxes, allocations, selected, busy, showSecrets, oauthing, runningWorkflowByEmail, onSelect, onOpenWorkflow, onOAuth, onDelete, onDone, onError }: {
  mailboxes: Mailbox[];
  allMailboxes: Mailbox[];
  allocations: GPTEmailAllocation[];
  selected?: string;
  busy: boolean;
  showSecrets: boolean;
  oauthing: string;
  runningWorkflowByEmail: Map<string, Job>;
  onSelect: (mailbox: Mailbox) => void;
  onOpenWorkflow: (job: Job) => void;
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
      <MailboxStatusStrip mailboxes={allMailboxes} allocations={allocations} />
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
              const currentWorkflow = runningWorkflowByEmail.get(normalizeUiEmail(mailbox.email_address));
              const allocation = allocationForEmail(allocations, mailbox.email_address);
              const usageStatus = allocation?.status || '-';
              const errorText = allocation?.last_error || mailbox.last_error || '-';
              return (
                <TableRow key={mailbox.email_address} className={selected === mailbox.email_address ? 'selected' : ''} onClick={() => onSelect(mailbox)}>
                  <TableCell data-label="主邮箱">
                    <div className="cellStack">
                      <span>{showSecrets ? mailbox.email_address : maskEmail(mailbox.email_address)}</span>
                      <small>{mailbox.primary_email || '-'}</small>
                    </div>
                  </TableCell>
                  <TableCell data-label="最近邮件"><MailboxActivityCell mailbox={mailbox} showSecrets={showSecrets} /></TableCell>
                  <TableCell data-label="占用"><StatusBadge status={usageStatus} /></TableCell>
                  <TableCell data-label="OAuth"><StatusBadge status={authStatus(mailbox)} /></TableCell>
                  <TableCell data-label="Token"><TokenBadge mailbox={mailbox} /></TableCell>
                  <TableCell data-label="更新">{formatUnix(mailbox.updated_at)}</TableCell>
                  <TableCell data-label="错误" className="mailboxErrorCell" title={errorText}>
                    <span>{compactCellError(errorText)}</span>
                  </TableCell>
                  <TableCell data-label="操作">
                    <div className="rowActions" onClick={(event) => event.stopPropagation()}>
                      {currentWorkflow ? (
                        <LinkedWorkflowButton job={currentWorkflow} onOpen={onOpenWorkflow} />
                      ) : canOAuth ? (
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

function WorkflowSummary({ job, runningCount, runningTitle, runningText, idleTitle, idleText }: {
  job?: Job;
  runningCount: number;
  runningTitle: (count: number) => string;
  runningText: string;
  idleTitle: string;
  idleText: string;
}) {
  const icon = runningCount > 0 ? <Clock size={16} /> : job?.status?.startsWith('FAILED') ? <AlertTriangle size={16} /> : <CheckCircle2 size={16} />;
  const title = runningCount > 0 ? runningTitle(runningCount) : job ? `最近一次：${statusText(job.status)}` : idleTitle;
  const text = runningCount > 0
    ? runningText
    : job
      ? `${actionText(job.action)} · ${stepText(job.last_step)}${job.error_message ? ` · ${compactCellError(job.error_message)}` : ''}`
      : idleText;

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

function GoPayStateStatusPanel({ state, loading }: { state: GoPayDashboardStateResponse | null; loading: boolean }) {
  const status = state?.status;
  const error = state?.error_message || state?.wa_phone_error_message || status?.error_message || '';
  const stage = String(status?.stage || '').trim();
  const waPhone = state?.wa_phone || '';
  const cls = error ? 'bad' : status?.token_present && stage === 'ready' ? 'good' : 'mid';
  const title = loading
    ? 'State 刷新中'
    : error
      ? 'State 异常'
      : status
        ? `State：${goPayStateStageText(stage)}`
        : 'State 未加载';
  const text = error
    ? compactCellError(error)
    : status
      ? `WA ${waPhone || '-'} · ${status.token_present ? 'Token 已保存' : '无 Token'} · ${goPayBalanceText(status)}`
      : '打开 GoPay 页后会读取 local state。';
  const icon = loading ? <Clock size={16} /> : error ? <AlertTriangle size={16} /> : <ShieldCheck size={16} />;
  const latestOtp = latestGoPayOtpWindow(status);

  return (
    <div className="goPayStatePanel">
      <div className={`registrationSummary ${cls}`}>
        {icon}
        <div>
          <strong>{title}</strong>
          <span title={error || text}>{text}</span>
        </div>
      </div>
      <div className="goPayStateGrid">
        <GoPayStateField label="User" value={state?.user_id || 'local'} />
        <GoPayStateField label="WA手机号" value={waPhone || '-'} />
        <GoPayStateField label="阶段" value={goPayStateStageText(stage)} raw={stage || '-'} />
        <GoPayStateField label="GoPay手机号" value={status?.phone || '-'} />
        <GoPayStateField label="Token" value={status?.token_present ? '已保存' : '-'} />
        <GoPayStateField label="余额" value={status ? goPayBalanceText(status) : '-'} />
        <GoPayStateField label="设备" value={status?.device_fingerprint || '-'} />
        <GoPayStateField label="OTP" value={latestOtp || '-'} />
        <GoPayStateField label="注销时间" value={formatUnix(status?.deactivated_at || 0)} />
      </div>
    </div>
  );
}

function GoPayStateField({ label, value, raw }: { label: string; value: string; raw?: string }) {
  return (
    <div className="goPayStateField" title={raw || value}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function goPayStateStageText(stage: string) {
  const labels: DisplayLabelMap = {
    ready: 'ready',
    login_otp_sent: '登录 OTP',
    signup_otp_sent: '注册 OTP',
    signup_pin_otp_sent: 'PIN OTP',
    change_phone_otp_sent: '换绑 OTP',
    deactivation_otp_sent: '注销 OTP'
  };
  return labels[stage] || stage || '未保存';
}

function goPayBalanceText(status: NonNullable<GoPayUserStatusResponse['status']>) {
  const currency = status.balance_currency || 'IDR';
  const amount = Number(status.balance_amount || 0);
  if (!status.token_present && amount === 0) return '-';
  return `${amount} ${currency}${status.has_min_balance ? ' · 足额' : ' · 未达标'}`;
}

function latestGoPayOtpWindow(status: GoPayUserStatusResponse['status']) {
  if (!status) return '';
  const windows = [
    { label: '登录', sent: status.login_otp_sent_at_unix, expires: status.login_otp_expires_at_unix },
    { label: '注册', sent: status.signup_otp_sent_at_unix, expires: status.signup_otp_expires_at_unix },
    { label: 'PIN', sent: status.signup_pin_otp_sent_at_unix, expires: status.signup_pin_otp_expires_at_unix }
  ].filter((item) => item.sent || item.expires);
  if (windows.length === 0) return '';
  windows.sort((a, b) => (b.sent || b.expires) - (a.sent || a.expires));
  const latest = windows[0];
  return `${latest.label} · ${formatUnix(latest.sent)} - ${formatUnix(latest.expires)}`;
}

function MailboxStatusStrip({ mailboxes, allocations }: { mailboxes: Mailbox[]; allocations: GPTEmailAllocation[] }) {
  const usageItems = ['AVAILABLE', 'ASSIGNED', 'REGISTERED', 'USER_ALREADY_EXISTS', 'REGISTRATION_FAILED', 'BLOCKED'];
  const authItems = ['AUTHORIZED', 'OAUTH_PENDING', 'AUTH_FAILED', 'NEEDS_MANUAL_VERIFICATION'];
  return (
    <div className="mailboxStatusStrip" aria-label="邮箱状态汇总">
      <div className="statusStripGroup">
        <h4>占用状态</h4>
        <div className="statusStripGrid">
          {usageItems.map((status) => (
            <div key={status}>
              <strong>{allocations.filter((allocation) => allocation.status === status).length}</strong>
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

function MailboxAliasesSection({ aliases, allocations, showSecrets, onDelete }: {
  aliases: Mailbox[];
  allocations: GPTEmailAllocation[];
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
        {aliases.map((alias) => {
          const allocation = allocationForEmail(allocations, alias.email_address);
          return (
            <div className="aliasItem" key={alias.email_address}>
              <div className="aliasIdentity">
                <strong>{showSecrets ? alias.email_address : maskEmail(alias.email_address)}</strong>
                <span><StatusBadge status={allocation?.status || '-'} /> <StatusBadge status={authStatus(alias)} /></span>
              </div>
              <MailboxActivityCell mailbox={alias} showSecrets={showSecrets} />
              <Button className="iconButton dangerButton" {...buttonHint('删除 Alias')} onClick={() => onDelete(alias)}>
                <Trash2 size={14} />
              </Button>
            </div>
          );
        })}
        {aliases.length === 0 && <div className="inboxEmpty">暂无 Alias 邮箱。</div>}
      </div>
    </section>
  );
}

function MailboxDetails({ mailbox, allocation, allocations, showSecrets, inboxResult, bans, aliases, inboxLoading, onCopy, onFetchInbox, onDelete }: {
  mailbox: Mailbox;
  allocation?: GPTEmailAllocation;
  allocations: GPTEmailAllocation[];
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
  const usageStatus = allocation?.status || '-';

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
                <StatusBadge status={usageStatus} />
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
          <KV label="占用" value={statusText(usageStatus)} copyValue={usageStatus} onCopy={onCopy} />
          <KV label="可分裂" value={allocation?.splittable ? '是' : '否'} onCopy={onCopy} />
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
          <KV label="错误" value={allocation?.last_error || mailbox.last_error || '-'} onCopy={onCopy} />
          <div className="buttonRow detailActions">
            <Button className="dangerButton" onClick={() => onDelete(mailbox)}>
              <Trash2 size={14} /> {mailbox.is_primary ? '删除主邮箱' : '删除 Alias'}
            </Button>
          </div>
        </section>
      )}

      {activeTab === 'aliases' && (
        <div className="mailboxTabPanel">
          <MailboxAliasesSection aliases={aliases} allocations={allocations} showSecrets={showSecrets} onDelete={onDelete} />
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

function ActivationChannelEditor({ account, activationChannel, onSave }: {
  account: Account;
  activationChannel: string;
  onSave: (account: Account, activationChannel: string) => Promise<void>;
}) {
  const stored = paymentChannelValue(account.activation_channel || '');
  const derived = paymentChannelValue(activationChannel);
  const current = stored || derived;
  const [value, setValue] = useState(current);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setValue(stored || derived);
  }, [account.account_id, account.activation_channel, stored, derived]);

  async function save() {
    setSaving(true);
    try {
      await onSave(account, value);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="editLine channelEditLine">
      <span>渠道</span>
      <NativeSelect value={value} onChange={(event) => setValue(paymentChannelValue(event.target.value))}>
        <NativeSelectOption value="">未设置</NativeSelectOption>
        <NativeSelectOption value="gopay_sms">Gopay-SMS</NativeSelectOption>
        <NativeSelectOption value="gopay_wa">Gopay-WA</NativeSelectOption>
      </NativeSelect>
      <Button {...buttonHint('保存渠道')} onClick={save} disabled={saving || value === stored}>
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

function eventText(eventType: string) {
  const labels: DisplayLabelMap = {
    job_created: '创建',
    job_updated: '更新',
    job_step_started: '步骤开始',
    job_step_progress: '步骤进度',
    job_step_completed: '步骤完成'
  };
  return labels[eventType] || eventType || '事件';
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

function canAutopay(account: Account) {
  const tier = normalizeTier(account.tier);
  return !isUserAlreadyExistsAccount(account) &&
    account.status !== 'ACTIVATED' &&
    !account.plus_active &&
    account.plus_trial_eligible !== false &&
    (tier === '' || tier === 'free') &&
    (!!account.session_token || !!account.access_token);
}

function canGoPayPayment(account: Account) {
  return canAutopay(account);
}

function accountActivationChannel(account: Account, jobs: Job[]) {
  const direct = goPayPaymentChannelLabel(paymentChannelValue(account.activation_channel || ''));
  if (direct !== '-') return direct;

  const latestPaymentJob = jobs
    .filter((job) =>
      job.account_id === account.account_id &&
      (job.action === 'GOPAY_PAYMENT' || job.action === 'ACTIVATE' || job.action === 'AUTOPAY' || job.action === 'REGISTER_AND_ACTIVATE')
    )
    .sort((a, b) => (b.updated_at || 0) - (a.updated_at || 0))[0];
  if (!latestPaymentJob) return '-';

  const result = objectValue(latestPaymentJob.result);
  return goPayPaymentChannelLabel(paymentChannelValue(stringValue(result.otp_channel)));
}

function paymentChannelValue(value: string): '' | 'gopay_sms' | 'gopay_wa' {
  const normalized = String(value || '').trim().toLowerCase();
  if (!normalized) return '';
  if (normalized === 'sms' || normalized === 'gopay_sms' || normalized === 'gopay-sms' || normalized.includes('gopay-sms') || normalized.includes('gopay_sms')) return 'gopay_sms';
  if (normalized === 'wa' || normalized === 'whatsapp' || normalized === 'gopay_wa' || normalized === 'gopay-wa' || normalized.includes('gopay-wa') || normalized.includes('gopay_wa') || normalized.includes('whatsapp')) return 'gopay_wa';
  if (normalized.includes('sms') && normalized.includes('gopay')) return 'gopay_sms';
  if (normalized.includes('wa') && normalized.includes('gopay')) return 'gopay_wa';
  return '';
}

function goPayAddBalancePayload(method: ConcreteGoPayAddBalanceMethod) {
  if (method === 'rekberinaja') return { rekberinaja: {} };
  if (method === 'envelope') return { envelope: {} };
  return { manual_transfer: {} };
}

function goPayPaymentChannelLabel(value: string) {
  const channel = paymentChannelValue(value);
  if (channel === 'gopay_sms') return 'Gopay-SMS';
  if (channel === 'gopay_wa') return 'Gopay-WA';
  return '-';
}

function isManualTransferActivation(value: string) {
  const normalized = String(value || '').trim().toLowerCase();
  return normalized === 'manual_transfer' || normalized === 'manual-transfer' || normalized.includes('manual_transfer') || normalized.includes('手动转账');
}

function isRekberinajaActivation(value: string) {
  const normalized = String(value || '').trim().toLowerCase();
  return normalized === 'rekberinaja' || normalized === 'r_platform' || normalized.includes('rekberinaja') || normalized.includes('r平台');
}

function isEnvelopeActivation(value: string) {
  const normalized = String(value || '').trim().toLowerCase();
  return normalized === 'envelope' || normalized === 'claim_envelope' || normalized.includes('envelope') || normalized.includes('红包');
}

function addBalanceMethodValue(value: string) {
  if (isRekberinajaActivation(value)) return 'rekberinaja';
  if (isEnvelopeActivation(value)) return 'envelope';
  if (isManualTransferActivation(value)) return 'manual_transfer';
  return '';
}

function addBalanceMethodLabel(value: string) {
  const method = addBalanceMethodValue(value);
  if (method === 'rekberinaja') return 'R平台';
  if (method === 'envelope') return '红包';
  if (method === 'manual_transfer') return '手动转账';
  return '';
}

function canProbeAccount(account: Account) {
  return !isUserAlreadyExistsAccount(account) && !!account.session_token;
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
  return job.status === 'RUNNING' && (job.action === 'REGISTER' || job.action === 'LOGIN_SESSION' || job.action === 'ACTIVATE' || job.action === 'AUTOPAY' || job.action === 'GOPAY_APP' || job.action === 'GOPAY_PAYMENT' || job.action === 'GOPAY_PAYMENT_REBIND' || job.action === 'REGISTER_AND_ACTIVATE');
}

function manualAddBalanceView(job: Job) {
  const data = stepResultData(job, 'gopay_app_add_balance');
  if (!data) return null;
  const transfer = objectValue(data.manual_transfer);
  return {
    method: stringValue(data.method),
    status: stringValue(data.status),
    transfer: {
      qr_payload: stringValue(transfer.qr_payload),
      instructions: stringValue(transfer.instructions),
      amount: numberValue(transfer.amount),
      currency: stringValue(transfer.currency) || 'IDR'
    }
  };
}

function canConfirmManualAddBalance(job: Job, progress: WorkflowProgress | null, balance: ReturnType<typeof manualAddBalanceView>) {
  return !!balance &&
    job.status === 'RUNNING' &&
    job.action === 'GOPAY_PAYMENT' &&
    balance.method === 'manual_transfer' &&
    (progress?.step_name === 'gopay_app_add_balance_confirm' || progress?.step_name === 'gopay_app_add_balance');
}

function canSelectGoPayAddBalance(job: Job, progress: WorkflowProgress | null, balance: ReturnType<typeof manualAddBalanceView>) {
  return job.status === 'RUNNING' &&
    job.action === 'GOPAY_PAYMENT' &&
    (progress?.step_name === 'gopay_app_add_balance' || job.last_step === 'gopay_app_add_balance') &&
    (!balance?.method || balance.status === 'awaiting_selection');
}

function canRetryGoPayPaymentRebind(job: Job) {
  const result = objectValue(job.result);
  if (job.action === 'GOPAY_PAYMENT_REBIND') {
    return job.status === 'FAILED_RETRYABLE' || job.status === 'FAILED_RECOVERABLE';
  }
  if (job.action !== 'GOPAY_PAYMENT') return false;
  const paymentCompleted = result.payment_completed === true || String(result.payment_completed || '').toLowerCase() === 'true';
  const hasPayment = !!(stringValue(result.charge_ref) || stringValue(result.snap_token));
  const changePhone = objectValue(result.change_phone);
  const changeComplete = result.change_phone_complete === true || changePhone.change_phone_complete === true;
  return paymentCompleted && hasPayment && !changeComplete && (job.status === 'SUCCEEDED' || job.status === 'FAILED_RECOVERABLE' || job.status === 'FAILED_RETRYABLE');
}

function goPayPaymentUserId(job: Job) {
  const result = objectValue(job.result);
  return stringValue(result.user_id) || 'local';
}

function stepResultData(job: Job, stepName: string): any | null {
  const step = (job.steps || []).find((item) => item.step_name === stepName);
  return stepDetailData(step);
}

function otpSubmitLabel(job: Job) {
  if (job.action === 'LOGIN_SESSION') return '登录 OTP';
  if (job.action === 'GOPAY_APP' || job.action === 'GOPAY_PAYMENT' || job.action === 'GOPAY_PAYMENT_REBIND') return 'GoPay OTP';
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

function mailboxContextForEmail(mailboxes: Mailbox[], allocations: GPTEmailAllocation[], email: string): AccountMailboxContext {
  const accountEmail = normalizeUiEmail(email);
  const mailbox = mailboxes.find((item) => normalizeUiEmail(item.email_address) === accountEmail);
  const allocation = allocationForEmail(allocations, accountEmail);
  const primaryEmail = normalizeUiEmail(allocation?.primary_email || mailbox?.primary_email || canonicalUiEmail(accountEmail));
  return {
    account_email: accountEmail,
    primary_email: primaryEmail,
    is_split: !!accountEmail && !!primaryEmail && accountEmail !== primaryEmail,
    known: !!mailbox || !!allocation
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

function countAllocatableEmailAllocations(allocations: GPTEmailAllocation[]) {
  return allocations.filter((allocation) => (
    allocation.status === 'AVAILABLE' ||
    (allocation.is_primary && allocation.status === 'REGISTERED' && allocation.splittable)
  )).length;
}

function mailboxMatchesFilter(mailbox: Mailbox, allMailboxes: Mailbox[], allocations: GPTEmailAllocation[], filter: string) {
  if (!filter) return true;
  const aliases = aliasesForMailbox(allMailboxes, mailbox);
  if (isAuthFilter(filter)) {
    return authStatus(mailbox) === filter || aliases.some((alias) => authStatus(alias) === filter);
  }
  const allocation = allocationForEmail(allocations, mailbox.email_address);
  return allocation?.status === filter ||
    allocations.some((item) => normalizeUiEmail(item.primary_email) === normalizeUiEmail(mailbox.email_address) && item.status === filter);
}

function allocationForEmail(allocations: GPTEmailAllocation[], email: string) {
  const target = normalizeUiEmail(email);
  if (!target) return undefined;
  return allocations.find((allocation) => normalizeUiEmail(allocation.email) === target);
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

function stepDuration(step: Step, nowUnix?: number) {
  if (!step.started_at) return null;
  const end = step.completed_at || nowUnix || Math.floor(Date.now() / 1000);
  const seconds = Math.max(0, end - step.started_at);
  if (seconds < 1) return <small className="stepTime">刚刚</small>;
  if (seconds < 60) return <small className="stepTime">{seconds}s</small>;
  return <small className="stepTime">{Math.floor(seconds / 60)}m {seconds % 60}s</small>;
}

function eventTime(event: JobEvent) {
  const snapshot = event.snapshot;
  const updated = snapshot?.progress?.updated_at_unix || snapshot?.job?.updated_at || 0;
  return formatUnix(updated);
}

function stepProgressText(step: Step, workflowProgress?: WorkflowProgress | null) {
  const data = stepDetailData(step);
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

function formatJSON(value: unknown) {
	try {
		return typeof value === 'string' ? JSON.stringify(JSON.parse(value), null, 2) : JSON.stringify(value, null, 2);
	} catch {
		return String(value ?? '');
	}
}

function stepDetailData(step?: Step): Record<string, any> | null {
  if (!step?.detail || typeof step.detail !== 'object') return null;
  return step.detail as Record<string, any>;
}

function stringValue(value: unknown) {
  return typeof value === 'string' ? value : '';
}

function objectValue(value: unknown): Record<string, any> {
  return value && typeof value === 'object' ? value as Record<string, any> : {};
}

function numberValue(value: unknown) {
  if (typeof value === 'number') return value;
  if (typeof value === 'string') {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : 0;
  }
  return 0;
}

function latestJobMap(jobs: Job[], keyOf: (job: Job) => string) {
  const map = new Map<string, Job>();
  for (const job of jobs) {
    const key = keyOf(job);
    if (!key) continue;
    const previous = map.get(key);
    if (!previous || (job.updated_at || 0) > (previous.updated_at || 0)) {
      map.set(key, job);
    }
  }
  return map;
}

function mailboxWorkflowEmail(job: Job) {
  if (job.action !== 'MAILBOX_OAUTH') return '';
  const candidates = [objectValue(job.result)];
  for (const step of job.steps || []) {
    const detail = stepDetailData(step);
    if (detail) candidates.push(detail);
  }
  for (const data of candidates) {
    const email = normalizeUiEmail(stringValue(data.email_address));
    if (email) return email;
  }
  return '';
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
  const text = String(value || '');
  if (!text) return false;

  let handledCopyEvent = false;
  const copyHandler = (event: ClipboardEvent) => {
    if (!event.clipboardData) return;
    event.clipboardData.setData('text/plain', text);
    event.preventDefault();
    handledCopyEvent = true;
  };
  try {
    document.addEventListener('copy', copyHandler);
    if (document.execCommand('copy') && handledCopyEvent) {
      return true;
    }
  } catch {
    // Fall through to textarea-based copy for older browsers.
  } finally {
    document.removeEventListener('copy', copyHandler);
  }

  const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const container = activeElement?.closest<HTMLElement>('[data-slot="sheet-content"], [role="dialog"]') || document.body;
  let textarea: HTMLTextAreaElement | null = null;
  try {
    textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', 'true');
    textarea.style.position = 'fixed';
    textarea.style.top = '0';
    textarea.style.left = '0';
    textarea.style.width = '1px';
    textarea.style.height = '1px';
    textarea.style.opacity = '0';
    textarea.style.pointerEvents = 'none';
    textarea.style.fontSize = '16px';
    container.appendChild(textarea);
    textarea.focus({ preventScroll: true });
    textarea.select();
    textarea.setSelectionRange(0, textarea.value.length);
    const copied = document.execCommand('copy');
    return copied;
  } catch {
    return false;
  } finally {
    if (textarea?.parentNode) {
      textarea.parentNode.removeChild(textarea);
    }
    try {
      activeElement?.focus({ preventScroll: true });
    } catch {
      activeElement?.focus();
    }
  }
}

createRoot(document.getElementById('root')!).render(
  <TooltipProvider>
    <App />
  </TooltipProvider>
);
