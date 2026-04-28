export namespace main {
	
	export class LoginRequest {
	    serverUrl: string;
	    username: string;
	    password: string;
	    skipTlsVerify: boolean;
	
	    static createFrom(source: any = {}) {
	        return new LoginRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverUrl = source["serverUrl"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.skipTlsVerify = source["skipTlsVerify"];
	    }
	}
	export class LoginResult {
	    serverUrl: string;
	    username: string;
	
	    static createFrom(source: any = {}) {
	        return new LoginResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serverUrl = source["serverUrl"];
	        this.username = source["username"];
	    }
	}
	export class UploadRequest {
	    remotePath: string;
	    filePath: string;
	    concurrency: number;
	
	    static createFrom(source: any = {}) {
	        return new UploadRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.remotePath = source["remotePath"];
	        this.filePath = source["filePath"];
	        this.concurrency = source["concurrency"];
	    }
	}
	export class UploadResult {
	    fileName: string;
	    size: number;
	    elapsed: string;
	
	    static createFrom(source: any = {}) {
	        return new UploadResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fileName = source["fileName"];
	        this.size = source["size"];
	        this.elapsed = source["elapsed"];
	    }
	}

}

