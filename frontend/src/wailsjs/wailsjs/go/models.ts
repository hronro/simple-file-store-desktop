export namespace main {
	
	export class AuthStatus {
	    loggedIn: boolean;
	    expiresAt: string;
	
	    static createFrom(source: any = {}) {
	        return new AuthStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.loggedIn = source["loggedIn"];
	        this.expiresAt = source["expiresAt"];
	    }
	}
	export class LoginRequest {
	    username: string;
	    password: string;
	
	    static createFrom(source: any = {}) {
	        return new LoginRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.username = source["username"];
	        this.password = source["password"];
	    }
	}
	export class UploadStartRequest {
	    filePaths: string[];
	    remoteDirectory: string;
	
	    static createFrom(source: any = {}) {
	        return new UploadStartRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filePaths = source["filePaths"];
	        this.remoteDirectory = source["remoteDirectory"];
	    }
	}
	export class UploadStartResponse {
	    jobId: string;
	
	    static createFrom(source: any = {}) {
	        return new UploadStartResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.jobId = source["jobId"];
	    }
	}

}

export namespace settings {
	
	export class Settings {
	    endpoint: string;
	    uploadThreads: number;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.endpoint = source["endpoint"];
	        this.uploadThreads = source["uploadThreads"];
	    }
	}

}

export namespace upload {
	
	export class JobState {
	    running: boolean;
	    jobId: string;
	
	    static createFrom(source: any = {}) {
	        return new JobState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.jobId = source["jobId"];
	    }
	}

}

