export namespace main {
	
	export class ModelInfo {
	    id: string;
	    name: string;
	    reasoning: boolean;
	    thinkingLevelMap?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.reasoning = source["reasoning"];
	        this.thinkingLevelMap = source["thinkingLevelMap"];
	    }
	}
	export class PersistedToolExecution {
	    id: string;
	    name: string;
	    result?: string;
	    isError?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PersistedToolExecution(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.result = source["result"];
	        this.isError = source["isError"];
	    }
	}
	export class PersistedToolCall {
	    id: string;
	    name: string;
	    arguments: string;
	
	    static createFrom(source: any = {}) {
	        return new PersistedToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.arguments = source["arguments"];
	    }
	}
	export class PersistedMessage {
	    id: string;
	    role: string;
	    content: string;
	    timestamp: string;
	    thinking?: string;
	    toolCalls?: PersistedToolCall[];
	    toolExecutions?: PersistedToolExecution[];
	
	    static createFrom(source: any = {}) {
	        return new PersistedMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.timestamp = source["timestamp"];
	        this.thinking = source["thinking"];
	        this.toolCalls = this.convertValues(source["toolCalls"], PersistedToolCall);
	        this.toolExecutions = this.convertValues(source["toolExecutions"], PersistedToolExecution);
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
	export class PersistedConversation {
	    id: string;
	    title: string;
	    messages: PersistedMessage[];
	    timestamp: string;
	
	    static createFrom(source: any = {}) {
	        return new PersistedConversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.messages = this.convertValues(source["messages"], PersistedMessage);
	        this.timestamp = source["timestamp"];
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
	
	export class PersistedSettings {
	    provider: string;
	    apiKey: string;
	    baseUrl: string;
	    model: string;
	    customModel: string;
	    workingDir: string;
	    ttsEnabled: boolean;
	    ttsVoice: string;
	    lastActiveConv?: string;
	    autoLearn?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PersistedSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.apiKey = source["apiKey"];
	        this.baseUrl = source["baseUrl"];
	        this.model = source["model"];
	        this.customModel = source["customModel"];
	        this.workingDir = source["workingDir"];
	        this.ttsEnabled = source["ttsEnabled"];
	        this.ttsVoice = source["ttsVoice"];
	        this.lastActiveConv = source["lastActiveConv"];
	        this.autoLearn = source["autoLearn"];
	    }
	}
	

}

