export namespace main {
	
	export class AgentApproval {
	    id: string;
	    sessionId: string;
	    projectId: string;
	    experimentId?: string;
	    appId?: string;
	    action: string;
	    resourceId: string;
	    requestJson: string;
	    status: string;
	    expiresAt: string;
	    decidedAt?: string;
	    consumedAt?: string;
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentApproval(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.projectId = source["projectId"];
	        this.experimentId = source["experimentId"];
	        this.appId = source["appId"];
	        this.action = source["action"];
	        this.resourceId = source["resourceId"];
	        this.requestJson = source["requestJson"];
	        this.status = source["status"];
	        this.expiresAt = source["expiresAt"];
	        this.decidedAt = source["decidedAt"];
	        this.consumedAt = source["consumedAt"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class AgentResourceSettings {
	    codexEnvironment: string;
	    claudeEnvironment: string;
	    opencodeEnvironment: string;
	    codexUnrestricted: boolean;
	    claudeUnrestricted: boolean;
	    opencodeUnrestricted: boolean;
	    sharedExtensions: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AgentResourceSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.codexEnvironment = source["codexEnvironment"];
	        this.claudeEnvironment = source["claudeEnvironment"];
	        this.opencodeEnvironment = source["opencodeEnvironment"];
	        this.codexUnrestricted = source["codexUnrestricted"];
	        this.claudeUnrestricted = source["claudeUnrestricted"];
	        this.opencodeUnrestricted = source["opencodeUnrestricted"];
	        this.sharedExtensions = source["sharedExtensions"];
	    }
	}
	export class AgentSession {
	    id: string;
	    projectId: string;
	    experimentId?: string;
	    spaceId: string;
	    agent: string;
	    name: string;
	    status: string;
	    terminalSessionId: string;
	    appId?: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.projectId = source["projectId"];
	        this.experimentId = source["experimentId"];
	        this.spaceId = source["spaceId"];
	        this.agent = source["agent"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.terminalSessionId = source["terminalSessionId"];
	        this.appId = source["appId"];
	    }
	}
	export class AppCandidate {
	    id: string;
	    name: string;
	    kind: string;
	    workingDirectory: string;
	    startCommand: string;
	    testCommand: string;
	    executable: string;
	    expectedPorts: number[];
	    suggestedHealthcheck: string;
	    framework: string;
	    confidence: number;
	    reason: string;
	    sourceFiles: string[];
	
	    static createFrom(source: any = {}) {
	        return new AppCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.workingDirectory = source["workingDirectory"];
	        this.startCommand = source["startCommand"];
	        this.testCommand = source["testCommand"];
	        this.executable = source["executable"];
	        this.expectedPorts = source["expectedPorts"];
	        this.suggestedHealthcheck = source["suggestedHealthcheck"];
	        this.framework = source["framework"];
	        this.confidence = source["confidence"];
	        this.reason = source["reason"];
	        this.sourceFiles = source["sourceFiles"];
	    }
	}
	export class AppInput {
	    projectId: string;
	    name: string;
	    kind: string;
	    workingDirectory: string;
	    startCommand: string;
	    stopCommand: string;
	    testCommand: string;
	    executable: string;
	    argumentsJson: string;
	    previewUrl: string;
	    healthcheckUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.workingDirectory = source["workingDirectory"];
	        this.startCommand = source["startCommand"];
	        this.stopCommand = source["stopCommand"];
	        this.testCommand = source["testCommand"];
	        this.executable = source["executable"];
	        this.argumentsJson = source["argumentsJson"];
	        this.previewUrl = source["previewUrl"];
	        this.healthcheckUrl = source["healthcheckUrl"];
	    }
	}
	export class ProjectApp {
	    id: string;
	    projectId: string;
	    experimentId?: string;
	    name: string;
	    kind: string;
	    workingDirectory: string;
	    startCommand: string;
	    stopCommand: string;
	    testCommand: string;
	    executable: string;
	    argumentsJson: string;
	    previewUrl: string;
	    healthcheckUrl: string;
	    status: string;
	    isPrimary: boolean;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectApp(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.projectId = source["projectId"];
	        this.experimentId = source["experimentId"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.workingDirectory = source["workingDirectory"];
	        this.startCommand = source["startCommand"];
	        this.stopCommand = source["stopCommand"];
	        this.testCommand = source["testCommand"];
	        this.executable = source["executable"];
	        this.argumentsJson = source["argumentsJson"];
	        this.previewUrl = source["previewUrl"];
	        this.healthcheckUrl = source["healthcheckUrl"];
	        this.status = source["status"];
	        this.isPrimary = source["isPrimary"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class AppRuntimeStatus {
	    app: ProjectApp;
	    experimentId?: string;
	    runtimeReference: string;
	    pid: number;
	    processAlive: boolean;
	    healthcheckPassed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AppRuntimeStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.app = this.convertValues(source["app"], ProjectApp);
	        this.experimentId = source["experimentId"];
	        this.runtimeReference = source["runtimeReference"];
	        this.pid = source["pid"];
	        this.processAlive = source["processAlive"];
	        this.healthcheckPassed = source["healthcheckPassed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Appearance {
	    mode: string;
	    accent: string;
	
	    static createFrom(source: any = {}) {
	        return new Appearance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.accent = source["accent"];
	    }
	}
	export class AttachRunningAppInput {
	    projectId: string;
	    spaceId: string;
	    appId: string;
	    terminalSessionId: string;
	    previewUrl: string;
	    detectedPort: number;
	    discoverySource: string;
	    confirmed: boolean;
	    name: string;
	    kind: string;
	    workingDirectory: string;
	
	    static createFrom(source: any = {}) {
	        return new AttachRunningAppInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.spaceId = source["spaceId"];
	        this.appId = source["appId"];
	        this.terminalSessionId = source["terminalSessionId"];
	        this.previewUrl = source["previewUrl"];
	        this.detectedPort = source["detectedPort"];
	        this.discoverySource = source["discoverySource"];
	        this.confirmed = source["confirmed"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.workingDirectory = source["workingDirectory"];
	    }
	}
	export class BrowserAutomationResult {
	    operation: string;
	    provider: string;
	    available: boolean;
	    success: boolean;
	    url?: string;
	    statusCode?: number;
	    durationMs: number;
	    screenshotPath?: string;
	    consoleErrors?: string[];
	    message?: string;
	    errorMessage?: string;
	
	    static createFrom(source: any = {}) {
	        return new BrowserAutomationResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.operation = source["operation"];
	        this.provider = source["provider"];
	        this.available = source["available"];
	        this.success = source["success"];
	        this.url = source["url"];
	        this.statusCode = source["statusCode"];
	        this.durationMs = source["durationMs"];
	        this.screenshotPath = source["screenshotPath"];
	        this.consoleErrors = source["consoleErrors"];
	        this.message = source["message"];
	        this.errorMessage = source["errorMessage"];
	    }
	}
	export class BrowserAutomationStatus {
	    provider: string;
	    available: boolean;
	    browserFeatures: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new BrowserAutomationStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.available = source["available"];
	        this.browserFeatures = source["browserFeatures"];
	        this.message = source["message"];
	    }
	}
	export class DuplicateVariant {
	    projectId: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new DuplicateVariant(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.label = source["label"];
	    }
	}
	export class DuplicateGroup {
	    key: string;
	    title: string;
	    variants: DuplicateVariant[];
	
	    static createFrom(source: any = {}) {
	        return new DuplicateGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.title = source["title"];
	        this.variants = this.convertValues(source["variants"], DuplicateVariant);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class EditorIntegration {
	    id: string;
	    name: string;
	    enabled: boolean;
	    available: boolean;
	    managed: boolean;
	    embedded: boolean;
	    status: string;
	    errorMessage: string;
	
	    static createFrom(source: any = {}) {
	        return new EditorIntegration(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.enabled = source["enabled"];
	        this.available = source["available"];
	        this.managed = source["managed"];
	        this.embedded = source["embedded"];
	        this.status = source["status"];
	        this.errorMessage = source["errorMessage"];
	    }
	}
	export class EditorSession {
	    sessionId: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new EditorSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.url = source["url"];
	    }
	}
	export class Experiment {
	    id: string;
	    projectId: string;
	    kind: string;
	    appId: string;
	    baseServerId?: string;
	    name: string;
	    objective: string;
	    baseBranch: string;
	    branchName: string;
	    baseCommit: string;
	    worktreePath: string;
	    status: string;
	    createdBy: string;
	    agentSessionId?: string;
	    riskLevel: string;
	    riskReasonsJson: string;
	    configurationJson: string;
	    createdAt: string;
	    updatedAt: string;
	    reviewReadyAt?: string;
	    integratedAt?: string;
	    discardedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new Experiment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.projectId = source["projectId"];
	        this.kind = source["kind"];
	        this.appId = source["appId"];
	        this.baseServerId = source["baseServerId"];
	        this.name = source["name"];
	        this.objective = source["objective"];
	        this.baseBranch = source["baseBranch"];
	        this.branchName = source["branchName"];
	        this.baseCommit = source["baseCommit"];
	        this.worktreePath = source["worktreePath"];
	        this.status = source["status"];
	        this.createdBy = source["createdBy"];
	        this.agentSessionId = source["agentSessionId"];
	        this.riskLevel = source["riskLevel"];
	        this.riskReasonsJson = source["riskReasonsJson"];
	        this.configurationJson = source["configurationJson"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.reviewReadyAt = source["reviewReadyAt"];
	        this.integratedAt = source["integratedAt"];
	        this.discardedAt = source["discardedAt"];
	    }
	}
	export class ExperimentChangeAnalysis {
	    planHash: string;
	    riskLevel: string;
	    reasons: string[];
	    recommendExperiment: boolean;
	    allowPrincipal: boolean;
	    needsAdvancedConfirm: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExperimentChangeAnalysis(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.planHash = source["planHash"];
	        this.riskLevel = source["riskLevel"];
	        this.reasons = source["reasons"];
	        this.recommendExperiment = source["recommendExperiment"];
	        this.allowPrincipal = source["allowPrincipal"];
	        this.needsAdvancedConfirm = source["needsAdvancedConfirm"];
	    }
	}
	export class ExperimentChangeInput {
	    projectId: string;
	    description: string;
	    areas: string[];
	    changeType: string;
	    migrations: string[];
	    dependencies: string[];
	    risks: string[];
	    context: string;
	    fileCount: number;
	
	    static createFrom(source: any = {}) {
	        return new ExperimentChangeInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.description = source["description"];
	        this.areas = source["areas"];
	        this.changeType = source["changeType"];
	        this.migrations = source["migrations"];
	        this.dependencies = source["dependencies"];
	        this.risks = source["risks"];
	        this.context = source["context"];
	        this.fileCount = source["fileCount"];
	    }
	}
	export class ExperimentCleanupInput {
	    experimentId: string;
	    confirmed: boolean;
	    backupDirty: boolean;
	    deleteBranch: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExperimentCleanupInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.experimentId = source["experimentId"];
	        this.confirmed = source["confirmed"];
	        this.backupDirty = source["backupDirty"];
	        this.deleteBranch = source["deleteBranch"];
	    }
	}
	export class ExperimentComparison {
	    experimentId: string;
	    baseCommit: string;
	    headCommit: string;
	    commitCount: number;
	    stat: string;
	    files: string;
	    patch: string;
	
	    static createFrom(source: any = {}) {
	        return new ExperimentComparison(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.experimentId = source["experimentId"];
	        this.baseCommit = source["baseCommit"];
	        this.headCommit = source["headCommit"];
	        this.commitCount = source["commitCount"];
	        this.stat = source["stat"];
	        this.files = source["files"];
	        this.patch = source["patch"];
	    }
	}
	export class ExperimentCreateInput {
	    projectId: string;
	    kind: string;
	    appId: string;
	    baseServerId: string;
	    name: string;
	    objective: string;
	    branchName: string;
	    createdBy: string;
	    agentSessionId: string;
	    riskLevel: string;
	    riskReasonsJson: string;
	    configurationJson: string;
	    confirmed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExperimentCreateInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.kind = source["kind"];
	        this.appId = source["appId"];
	        this.baseServerId = source["baseServerId"];
	        this.name = source["name"];
	        this.objective = source["objective"];
	        this.branchName = source["branchName"];
	        this.createdBy = source["createdBy"];
	        this.agentSessionId = source["agentSessionId"];
	        this.riskLevel = source["riskLevel"];
	        this.riskReasonsJson = source["riskReasonsJson"];
	        this.configurationJson = source["configurationJson"];
	        this.confirmed = source["confirmed"];
	    }
	}
	export class ExperimentReview {
	    experiment: Experiment;
	    comparison: ExperimentComparison;
	    testsPassed: boolean;
	    testOutput: string;
	    appVerified: boolean;
	    secretFindings: string[];
	    conflicts: string[];
	    integrationPath: string;
	    mainHead: string;
	    reproducibleVerified: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExperimentReview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.experiment = this.convertValues(source["experiment"], Experiment);
	        this.comparison = this.convertValues(source["comparison"], ExperimentComparison);
	        this.testsPassed = source["testsPassed"];
	        this.testOutput = source["testOutput"];
	        this.appVerified = source["appVerified"];
	        this.secretFindings = source["secretFindings"];
	        this.conflicts = source["conflicts"];
	        this.integrationPath = source["integrationPath"];
	        this.mainHead = source["mainHead"];
	        this.reproducibleVerified = source["reproducibleVerified"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ServerStats {
	    cpuPercent: number;
	    memoryUsedMb: number;
	    memoryLimitMb: number;
	    limitsEnforced: boolean;
	    limitDescription: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cpuPercent = source["cpuPercent"];
	        this.memoryUsedMb = source["memoryUsedMb"];
	        this.memoryLimitMb = source["memoryLimitMb"];
	        this.limitsEnforced = source["limitsEnforced"];
	        this.limitDescription = source["limitDescription"];
	    }
	}
	export class GlobalServer {
	    id: string;
	    projectId: string;
	    appId: string;
	    experimentId?: string;
	    baseServerId?: string;
	    name: string;
	    provider: string;
	    distro: string;
	    runtimeReference: string;
	    status: string;
	    cpuLimit: number;
	    memoryMb: number;
	    diskGb: number;
	    keepAlive: boolean;
	    createdAt: string;
	    updatedAt: string;
	    projectName: string;
	    appName: string;
	    stats?: ServerStats;
	
	    static createFrom(source: any = {}) {
	        return new GlobalServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.projectId = source["projectId"];
	        this.appId = source["appId"];
	        this.experimentId = source["experimentId"];
	        this.baseServerId = source["baseServerId"];
	        this.name = source["name"];
	        this.provider = source["provider"];
	        this.distro = source["distro"];
	        this.runtimeReference = source["runtimeReference"];
	        this.status = source["status"];
	        this.cpuLimit = source["cpuLimit"];
	        this.memoryMb = source["memoryMb"];
	        this.diskGb = source["diskGb"];
	        this.keepAlive = source["keepAlive"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.projectName = source["projectName"];
	        this.appName = source["appName"];
	        this.stats = this.convertValues(source["stats"], ServerStats);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ManagedAppDetection {
	    terminalSessionId: string;
	    processId: number;
	    port: number;
	    previewUrl: string;
	    verified: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ManagedAppDetection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.terminalSessionId = source["terminalSessionId"];
	        this.processId = source["processId"];
	        this.port = source["port"];
	        this.previewUrl = source["previewUrl"];
	        this.verified = source["verified"];
	        this.message = source["message"];
	    }
	}
	export class MediaPlaybackState {
	    available: boolean;
	    source: string;
	    title: string;
	    artist: string;
	    album: string;
	    artworkDataURL: string;
	    playbackStatus: string;
	    positionSeconds: number;
	    durationSeconds: number;
	    canToggle: boolean;
	    canNext: boolean;
	    canPrevious: boolean;
	    errorMessage: string;
	    trackKey: string;
	
	    static createFrom(source: any = {}) {
	        return new MediaPlaybackState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.source = source["source"];
	        this.title = source["title"];
	        this.artist = source["artist"];
	        this.album = source["album"];
	        this.artworkDataURL = source["artworkDataURL"];
	        this.playbackStatus = source["playbackStatus"];
	        this.positionSeconds = source["positionSeconds"];
	        this.durationSeconds = source["durationSeconds"];
	        this.canToggle = source["canToggle"];
	        this.canNext = source["canNext"];
	        this.canPrevious = source["canPrevious"];
	        this.errorMessage = source["errorMessage"];
	        this.trackKey = source["trackKey"];
	    }
	}
	export class Project {
	    id: string;
	    name: string;
	    path: string;
	    source: string;
	    gitRemote?: string;
	    branch?: string;
	    favorite: boolean;
	    archived: boolean;
	    createdAt: string;
	    updatedAt: string;
	    groupId?: string;
	    groupTitle?: string;
	    variantLabel?: string;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.source = source["source"];
	        this.gitRemote = source["gitRemote"];
	        this.branch = source["branch"];
	        this.favorite = source["favorite"];
	        this.archived = source["archived"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.groupId = source["groupId"];
	        this.groupTitle = source["groupTitle"];
	        this.variantLabel = source["variantLabel"];
	    }
	}
	
	export class ProjectCandidate {
	    id: string;
	    name: string;
	    path: string;
	    gitRemote?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.gitRemote = source["gitRemote"];
	    }
	}
	export class ProjectContext {
	    projectId: string;
	    experimentId: string;
	    name: string;
	    kind: string;
	    branchName: string;
	    path: string;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectContext(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.experimentId = source["experimentId"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.branchName = source["branchName"];
	        this.path = source["path"];
	        this.status = source["status"];
	    }
	}
	export class ProjectTerminalSession {
	    sessionId: string;
	    projectId: string;
	    experimentId?: string;
	    spaceId: string;
	    shell: string;
	    agent?: string;
	    status: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectTerminalSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.projectId = source["projectId"];
	        this.experimentId = source["experimentId"];
	        this.spaceId = source["spaceId"];
	        this.shell = source["shell"];
	        this.agent = source["agent"];
	        this.status = source["status"];
	        this.name = source["name"];
	    }
	}
	export class Server {
	    id: string;
	    projectId: string;
	    appId: string;
	    experimentId?: string;
	    baseServerId?: string;
	    name: string;
	    provider: string;
	    distro: string;
	    runtimeReference: string;
	    status: string;
	    cpuLimit: number;
	    memoryMb: number;
	    diskGb: number;
	    keepAlive: boolean;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Server(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.projectId = source["projectId"];
	        this.appId = source["appId"];
	        this.experimentId = source["experimentId"];
	        this.baseServerId = source["baseServerId"];
	        this.name = source["name"];
	        this.provider = source["provider"];
	        this.distro = source["distro"];
	        this.runtimeReference = source["runtimeReference"];
	        this.status = source["status"];
	        this.cpuLimit = source["cpuLimit"];
	        this.memoryMb = source["memoryMb"];
	        this.diskGb = source["diskGb"];
	        this.keepAlive = source["keepAlive"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class ServerConnection {
	    id: string;
	    serverId: string;
	    sourceServiceId?: string;
	    targetServiceId?: string;
	    protocol: string;
	    port?: number;
	    status: string;
	    source: string;
	    trafficRate: number;
	    errorRate: number;
	    metadataJson: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerConnection(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.serverId = source["serverId"];
	        this.sourceServiceId = source["sourceServiceId"];
	        this.targetServiceId = source["targetServiceId"];
	        this.protocol = source["protocol"];
	        this.port = source["port"];
	        this.status = source["status"];
	        this.source = source["source"];
	        this.trafficRate = source["trafficRate"];
	        this.errorRate = source["errorRate"];
	        this.metadataJson = source["metadataJson"];
	    }
	}
	export class ServerConnectionInput {
	    id: string;
	    projectId: string;
	    serverId: string;
	    sourceServiceId: string;
	    targetServiceId: string;
	    protocol: string;
	    port?: number;
	    metadataJson: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerConnectionInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.projectId = source["projectId"];
	        this.serverId = source["serverId"];
	        this.sourceServiceId = source["sourceServiceId"];
	        this.targetServiceId = source["targetServiceId"];
	        this.protocol = source["protocol"];
	        this.port = source["port"];
	        this.metadataJson = source["metadataJson"];
	    }
	}
	export class ServerHealth {
	    healthy: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerHealth(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.healthy = source["healthy"];
	        this.message = source["message"];
	    }
	}
	export class ServerInput {
	    projectId: string;
	    appId: string;
	    experimentId: string;
	    baseServerId: string;
	    name: string;
	    provider: string;
	    distro: string;
	    cpuLimit: number;
	    memoryMb: number;
	    diskGb: number;
	    keepAlive: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ServerInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.appId = source["appId"];
	        this.experimentId = source["experimentId"];
	        this.baseServerId = source["baseServerId"];
	        this.name = source["name"];
	        this.provider = source["provider"];
	        this.distro = source["distro"];
	        this.cpuLimit = source["cpuLimit"];
	        this.memoryMb = source["memoryMb"];
	        this.diskGb = source["diskGb"];
	        this.keepAlive = source["keepAlive"];
	    }
	}
	export class ServerService {
	    id: string;
	    serverId: string;
	    name: string;
	    kind: string;
	    host: string;
	    port?: number;
	    protocol: string;
	    healthcheckUrl: string;
	    status: string;
	    source: string;
	    metadataJson: string;
	    positionJson: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.serverId = source["serverId"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.protocol = source["protocol"];
	        this.healthcheckUrl = source["healthcheckUrl"];
	        this.status = source["status"];
	        this.source = source["source"];
	        this.metadataJson = source["metadataJson"];
	        this.positionJson = source["positionJson"];
	    }
	}
	export class ServerReproducibleExport {
	    experiment: Experiment;
	    server: Server;
	    files: string[];
	    health: ServerHealth;
	    services: ServerService[];
	    connections: ServerConnection[];
	    rebuilt: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ServerReproducibleExport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.experiment = this.convertValues(source["experiment"], Experiment);
	        this.server = this.convertValues(source["server"], Server);
	        this.files = source["files"];
	        this.health = this.convertValues(source["health"], ServerHealth);
	        this.services = this.convertValues(source["services"], ServerService);
	        this.connections = this.convertValues(source["connections"], ServerConnection);
	        this.rebuilt = source["rebuilt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ServerServiceInput {
	    id: string;
	    projectId: string;
	    serverId: string;
	    name: string;
	    kind: string;
	    host: string;
	    port?: number;
	    protocol: string;
	    healthcheckUrl: string;
	    metadataJson: string;
	    positionJson: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerServiceInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.projectId = source["projectId"];
	        this.serverId = source["serverId"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.protocol = source["protocol"];
	        this.healthcheckUrl = source["healthcheckUrl"];
	        this.metadataJson = source["metadataJson"];
	        this.positionJson = source["positionJson"];
	    }
	}
	
	export class TopologyHealthcheckResult {
	    sequenceId: string;
	    serverId: string;
	    serviceId?: string;
	    connectionId?: string;
	    healthy: boolean;
	    statusCode?: number;
	    durationMs: number;
	    message: string;
	    checkedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new TopologyHealthcheckResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sequenceId = source["sequenceId"];
	        this.serverId = source["serverId"];
	        this.serviceId = source["serviceId"];
	        this.connectionId = source["connectionId"];
	        this.healthy = source["healthy"];
	        this.statusCode = source["statusCode"];
	        this.durationMs = source["durationMs"];
	        this.message = source["message"];
	        this.checkedAt = source["checkedAt"];
	    }
	}
	export class WSLDistributionResource {
	    id: string;
	    name: string;
	    runtimeName: string;
	    selected: boolean;
	    installed: boolean;
	    status: string;
	    errorMessage: string;
	
	    static createFrom(source: any = {}) {
	        return new WSLDistributionResource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.runtimeName = source["runtimeName"];
	        this.selected = source["selected"];
	        this.installed = source["installed"];
	        this.status = source["status"];
	        this.errorMessage = source["errorMessage"];
	    }
	}
	export class WorkspacePhotoAsset {
	    assetId: string;
	    dataURL: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkspacePhotoAsset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.assetId = source["assetId"];
	        this.dataURL = source["dataURL"];
	    }
	}

}

