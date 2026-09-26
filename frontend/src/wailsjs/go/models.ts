export namespace flasher {
	
	export class BackupFile {
	    device: string;
	    name: string;
	    path: string;
	    size: number;
	    sha256: string;
	
	    static createFrom(source: any = {}) {
	        return new BackupFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.device = source["device"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.size = source["size"];
	        this.sha256 = source["sha256"];
	    }
	}
	export class BackupResult {
	    directory: string;
	    manifestPath: string;
	    model: string;
	    host: string;
	    files: BackupFile[];
	
	    static createFrom(source: any = {}) {
	        return new BackupResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.manifestPath = source["manifestPath"];
	        this.model = source["model"];
	        this.host = source["host"];
	        this.files = this.convertValues(source["files"], BackupFile);
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
	export class FileInfo {
	    path: string;
	    name: string;
	    size: number;
	    sha256: string;
	
	    static createFrom(source: any = {}) {
	        return new FileInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.size = source["size"];
	        this.sha256 = source["sha256"];
	    }
	}
	export class SSHConfig {
	    host: string;
	    username: string;
	    password: string;
	
	    static createFrom(source: any = {}) {
	        return new SSHConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.username = source["username"];
	        this.password = source["password"];
	    }
	}
	export class FlashInput {
	    model: string;
	    ssh: SSHConfig;
	    mibibPath: string;
	    ubootPath: string;
	
	    static createFrom(source: any = {}) {
	        return new FlashInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.ssh = this.convertValues(source["ssh"], SSHConfig);
	        this.mibibPath = source["mibibPath"];
	        this.ubootPath = source["ubootPath"];
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
	export class FlashResult {
	    mibibSHA256: string;
	    ubootSHA256: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new FlashResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mibibSHA256 = source["mibibSHA256"];
	        this.ubootSHA256 = source["ubootSHA256"];
	        this.message = source["message"];
	    }
	}
	export class Materials {
	    developerFirmware: FileInfo;
	    mibib: FileInfo;
	    uboot: FileInfo;
	    firmware: FileInfo;
	    firmwareBoard: string;
	    firmwareLayout: string;
	    backupFiles: FileInfo[];
	
	    static createFrom(source: any = {}) {
	        return new Materials(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.developerFirmware = this.convertValues(source["developerFirmware"], FileInfo);
	        this.mibib = this.convertValues(source["mibib"], FileInfo);
	        this.uboot = this.convertValues(source["uboot"], FileInfo);
	        this.firmware = this.convertValues(source["firmware"], FileInfo);
	        this.firmwareBoard = source["firmwareBoard"];
	        this.firmwareLayout = source["firmwareLayout"];
	        this.backupFiles = this.convertValues(source["backupFiles"], FileInfo);
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
	export class ProbeResult {
	    url: string;
	    kind: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ProbeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.kind = source["kind"];
	        this.message = source["message"];
	    }
	}
	export class RouterInfo {
	    model: string;
	    modelKey: string;
	    mtd: Record<string, string>;
	    rawMtd: string;
	    host: string;
	    compatible: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RouterInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.modelKey = source["modelKey"];
	        this.mtd = source["mtd"];
	        this.rawMtd = source["rawMtd"];
	        this.host = source["host"];
	        this.compatible = source["compatible"];
	    }
	}

}

export namespace main {
	
	export class Adapter {
	    name: string;
	    ipv4: string[];
	    flags: string;
	
	    static createFrom(source: any = {}) {
	        return new Adapter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.ipv4 = source["ipv4"];
	        this.flags = source["flags"];
	    }
	}
	export class DefaultPaths {
	    model: string;
	    programDir: string;
	    toolsDir: string;
	    unlockTool: string;
	    developerFirmware: string;
	    mibib: string;
	    uboot: string;
	    firmware: string;
	    backupBaseDir: string;
	
	    static createFrom(source: any = {}) {
	        return new DefaultPaths(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.programDir = source["programDir"];
	        this.toolsDir = source["toolsDir"];
	        this.unlockTool = source["unlockTool"];
	        this.developerFirmware = source["developerFirmware"];
	        this.mibib = source["mibib"];
	        this.uboot = source["uboot"];
	        this.firmware = source["firmware"];
	        this.backupBaseDir = source["backupBaseDir"];
	    }
	}
	export class WorkflowState {
	    model: string;
	    lastStep: number;
	    ubootWritten: boolean;
	    physicalConfirmed: boolean;
	    firmwareUploaded: boolean;
	    backupDir: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.lastStep = source["lastStep"];
	        this.ubootWritten = source["ubootWritten"];
	        this.physicalConfirmed = source["physicalConfirmed"];
	        this.firmwareUploaded = source["firmwareUploaded"];
	        this.backupDir = source["backupDir"];
	        this.updatedAt = source["updatedAt"];
	    }
	}

}

