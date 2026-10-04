export namespace main {
	
	export class DonateAddresses {
	    ethereum: string;
	    bitcoin: string;
	
	    static createFrom(source: any = {}) {
	        return new DonateAddresses(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ethereum = source["ethereum"];
	        this.bitcoin = source["bitcoin"];
	    }
	}
	export class DroppedField {
	    field: string;
	    note: string;
	
	    static createFrom(source: any = {}) {
	        return new DroppedField(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.field = source["field"];
	        this.note = source["note"];
	    }
	}
	export class FieldMapping {
	    from: string;
	    to: string;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new FieldMapping(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.note = source["note"];
	    }
	}
	export class DryRunPlan {
	    dry_run: boolean;
	    source: string;
	    target_version: string;
	    input_file: string;
	    trajectories: number;
	    runs_read: number;
	    spans_planned: number;
	    field_mappings: FieldMapping[];
	    dropped_fields: DroppedField[];
	    sample_span?: number[];
	
	    static createFrom(source: any = {}) {
	        return new DryRunPlan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dry_run = source["dry_run"];
	        this.source = source["source"];
	        this.target_version = source["target_version"];
	        this.input_file = source["input_file"];
	        this.trajectories = source["trajectories"];
	        this.runs_read = source["runs_read"];
	        this.spans_planned = source["spans_planned"];
	        this.field_mappings = this.convertValues(source["field_mappings"], FieldMapping);
	        this.dropped_fields = this.convertValues(source["dropped_fields"], DroppedField);
	        this.sample_span = source["sample_span"];
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
	export class EvalConfig {
	    DB: string;
	    Trajectory: string;
	    Assertions: string;
	    JudgesRun: string;
	    JudgeEndpoint: string;
	    JudgeAPIKey: string;
	
	    static createFrom(source: any = {}) {
	        return new EvalConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.DB = source["DB"];
	        this.Trajectory = source["Trajectory"];
	        this.Assertions = source["Assertions"];
	        this.JudgesRun = source["JudgesRun"];
	        this.JudgeEndpoint = source["JudgeEndpoint"];
	        this.JudgeAPIKey = source["JudgeAPIKey"];
	    }
	}
	export class EvalResultView {
	    assertion_id: string;
	    assertion_version: string;
	    status: string;
	    detail?: string;
	
	    static createFrom(source: any = {}) {
	        return new EvalResultView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.assertion_id = source["assertion_id"];
	        this.assertion_version = source["assertion_version"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	    }
	}
	
	export class JudgeView {
	    id: string;
	    model_fingerprint: string;
	    description: string;
	    provider?: string;
	
	    static createFrom(source: any = {}) {
	        return new JudgeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.model_fingerprint = source["model_fingerprint"];
	        this.description = source["description"];
	        this.provider = source["provider"];
	    }
	}
	export class ProviderView {
	    endpoint: string;
	    api_key_env?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.endpoint = source["endpoint"];
	        this.api_key_env = source["api_key_env"];
	    }
	}
	export class JudgeManifestView {
	    namespace: string;
	    version: string;
	    providers: Record<string, ProviderView>;
	    judges: JudgeView[];
	
	    static createFrom(source: any = {}) {
	        return new JudgeManifestView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.namespace = source["namespace"];
	        this.version = source["version"];
	        this.providers = this.convertValues(source["providers"], ProviderView, true);
	        this.judges = this.convertValues(source["judges"], JudgeView);
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
	
	export class MigrateConfig {
	    Source: string;
	    File: string;
	    DB: string;
	    DryRun: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MigrateConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Source = source["Source"];
	        this.File = source["File"];
	        this.DB = source["DB"];
	        this.DryRun = source["DryRun"];
	    }
	}
	
	export class ReportConfig {
	    Source: string;
	    File: string;
	    OutDir: string;
	
	    static createFrom(source: any = {}) {
	        return new ReportConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Source = source["Source"];
	        this.File = source["File"];
	        this.OutDir = source["OutDir"];
	    }
	}
	export class ServeConfig {
	    DB: string;
	    Addr: string;
	    SCIM: boolean;
	    SCIMToken: string;
	
	    static createFrom(source: any = {}) {
	        return new ServeConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.DB = source["DB"];
	        this.Addr = source["Addr"];
	        this.SCIM = source["SCIM"];
	        this.SCIMToken = source["SCIMToken"];
	    }
	}
	export class SpanView {
	    span_id: string;
	    trajectory_id: string;
	    name: string;
	    kind: string;
	    started_at_unix_ms: number;
	    ended_at_unix_ms: number;
	    input: string;
	    output: string;
	    raw?: number[];
	
	    static createFrom(source: any = {}) {
	        return new SpanView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.span_id = source["span_id"];
	        this.trajectory_id = source["trajectory_id"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.started_at_unix_ms = source["started_at_unix_ms"];
	        this.ended_at_unix_ms = source["ended_at_unix_ms"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.raw = source["raw"];
	    }
	}
	export class TrajectoryBundle {
	    trajectory: number[];
	    spans: SpanView[];
	
	    static createFrom(source: any = {}) {
	        return new TrajectoryBundle(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.trajectory = source["trajectory"];
	        this.spans = this.convertValues(source["spans"], SpanView);
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
	export class TrajectoryReportView {
	    trajectory_id: string;
	    results: EvalResultView[];
	    total: number;
	    passed: number;
	    failed: number;
	    errored: number;
	
	    static createFrom(source: any = {}) {
	        return new TrajectoryReportView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.trajectory_id = source["trajectory_id"];
	        this.results = this.convertValues(source["results"], EvalResultView);
	        this.total = source["total"];
	        this.passed = source["passed"];
	        this.failed = source["failed"];
	        this.errored = source["errored"];
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
	export class WizardStepView {
	    step: number;
	    title: string;
	    body: string;
	    source: string;
	    file: string;
	    db: string;
	    needsFile: boolean;
	    runnable: boolean;
	    ranStep: boolean;
	    running: boolean;
	    summary: string;
	    tail: string[];
	    done?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new WizardStepView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.step = source["step"];
	        this.title = source["title"];
	        this.body = source["body"];
	        this.source = source["source"];
	        this.file = source["file"];
	        this.db = source["db"];
	        this.needsFile = source["needsFile"];
	        this.runnable = source["runnable"];
	        this.ranStep = source["ranStep"];
	        this.running = source["running"];
	        this.summary = source["summary"];
	        this.tail = source["tail"];
	        this.done = source["done"];
	    }
	}

}

